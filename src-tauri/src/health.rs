//! 后台健康巡检：周期探测实例 API 端口可达性，失败达阈值自动重启。
//!
//! 探测方式：TCP connect（1s 超时）到实例 API 端口——实例存在 401 门禁时
//! HTTP GET 需要携带密钥，TCP 连通性即可判定进程存活且端口在监听，
//! 与 `start_instance_inner` 中 `wait_for_port` 的判据一致。

use crate::core::AppCore;
use std::io::ErrorKind;
use std::net::{SocketAddr, TcpStream};
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

/// 单实例健康记录（持久化于 health.json）
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct HealthRecord {
    pub name: String,
    pub healthy: bool,
    pub last_check_ts: i64,
    pub consecutive_failures: u32,
    pub last_error: Option<String>,
}

/// 巡检汇总（前端展示）
#[derive(Debug, Default, serde::Serialize)]
pub struct HealthSummary {
    pub total: usize,
    pub healthy: usize,
    pub unhealthy: usize,
    pub records: Vec<HealthRecord>,
    pub last_scan_ts: i64,
}

fn now_ts() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

fn health_file_path() -> std::path::PathBuf {
    let (_, _, runtime_dir) = crate::commands::manager_paths();
    runtime_dir.join("health.json")
}

fn load_records() -> Vec<HealthRecord> {
    std::fs::read_to_string(health_file_path())
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

fn save_records(records: &[HealthRecord]) {
    let path = health_file_path();
    if let Some(dir) = path.parent() {
        let _ = std::fs::create_dir_all(dir);
    }
    if let Ok(json) = serde_json::to_string_pretty(records) {
        let _ = std::fs::write(path, json);
    }
}

/// 探测单个实例 API 端口是否可连接
fn probe_port(port: u16, timeout: Duration) -> bool {
    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    match TcpStream::connect_timeout(&addr, timeout) {
        Ok(_) => true,
        Err(e) if e.kind() == ErrorKind::TimedOut || e.kind() == ErrorKind::ConnectionRefused => false,
        Err(_) => false,
    }
}

/// 单轮巡检：探测运行中实例的 API 端口
pub fn run_health_check_once(core: &AppCore) -> HealthSummary {
    let mut records = load_records();
    let mut by_name: std::collections::HashMap<String, HealthRecord> =
        records.drain(..).map(|r| (r.name.clone(), r)).collect();

    let instances: Vec<crate::instance::Instance> = core
        .manager
        .lock()
        .map(|m| m.list_instances().to_vec())
        .unwrap_or_default();

    let now = now_ts();
    let mut summary = HealthSummary::default();
    summary.last_scan_ts = now;

    // 仅巡检当前 Running 的实例
    let running: Vec<crate::instance::Instance> = instances
        .iter()
        .filter(|i| matches!(i.status, crate::instance::InstanceStatus::Running))
        .cloned()
        .collect();

    for inst in &running {
        let healthy = probe_port(inst.port, Duration::from_secs(1));
        let prev = by_name.entry(inst.name.clone()).or_insert(HealthRecord {
            name: inst.name.clone(),
            healthy: true,
            last_check_ts: now,
            consecutive_failures: 0,
            last_error: None,
        });
        prev.last_check_ts = now;
        if healthy {
            prev.healthy = true;
            prev.consecutive_failures = 0;
            prev.last_error = None;
        } else {
            prev.healthy = false;
            prev.consecutive_failures += 1;
            prev.last_error = Some(format!("API 端口 127.0.0.1:{} 不可达", inst.port));
        }
    }

    // 依据配置自动重启：仅对「当前 Running 且连续失败达阈值」的实例重启，
    // 避免把已停止实例（含陈旧的失败计数记录）误启。
    let threshold = crate::config::Config::load()
        .unwrap_or_default()
        .health_restart_threshold
        .unwrap_or(0);
    let running_names: std::collections::HashSet<String> =
        running.iter().map(|i| i.name.clone()).collect();
    let mut to_restart: Vec<String> = Vec::new();
    for rec in by_name.values() {
        if running_names.contains(&rec.name)
            && threshold > 0
            && rec.consecutive_failures >= threshold
        {
            to_restart.push(rec.name.clone());
        }
    }
    for name in to_restart {
        let stopped = crate::commands::stop_instance_core(core, &name).is_ok();
        let started = stopped && crate::commands::start_instance_core(core, &name).is_ok();
        if let Some(rec) = by_name.get_mut(&name) {
            if started {
                rec.healthy = true;
                rec.consecutive_failures = 0;
                rec.last_error = Some("已自动重启".to_string());
            } else {
                rec.healthy = false;
                rec.last_error = Some("自动重启失败".to_string());
            }
        }
    }

    let mut records_out: Vec<HealthRecord> = by_name.into_values().collect();
    // 只保留当前 Running 实例的记录：已停止/已删除实例的陈旧记录会污染
    // total/healthy/unhealthy 统计（例如停掉一个实例后仍显示其旧健康记录）。
    records_out.retain(|r| running_names.contains(&r.name));
    records_out.sort_by(|a, b| a.name.cmp(&b.name));
    summary.total = running.len();
    summary.healthy = records_out.iter().filter(|r| r.healthy).count();
    summary.unhealthy = records_out.iter().filter(|r| !r.healthy).count();
    summary.records = records_out.clone();
    save_records(&records_out);
    summary
}

/// 后台巡检循环（由桌面 run() 与 headless 入口按配置间隔 spawn）
pub async fn health_loop(core: Arc<AppCore>) {
    loop {
        let interval = crate::config::Config::load()
            .unwrap_or_default()
            .health_check_interval_sec
            .unwrap_or(0);
        if interval == 0 {
            tokio::time::sleep(Duration::from_secs(30)).await;
            continue;
        }
        let started = Instant::now();
        let core2 = core.clone();
        let _ = tokio::task::spawn_blocking(move || run_health_check_once(&core2)).await;
        let elapsed = started.elapsed().as_secs();
        let sleep = Duration::from_secs(interval.saturating_sub(elapsed as u32) as u64);
        tokio::time::sleep(sleep).await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_probe_port_closed() {
        // 未监听端口应判定不可达（探测 1 个不存在的端口）
        let ok = probe_port(1, Duration::from_millis(200));
        assert!(!ok);
    }

    #[test]
    fn test_health_file_roundtrip() {
        // health_file_path() 依赖 OPCODE2API_DATA_DIR，与改写 env 的并发测试
        // 需串行（共享 CONFIG_TEST_LOCK），否则读到对方隔离目录。
        let _guard = crate::config::CONFIG_TEST_LOCK
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        let orig = std::env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = std::env::temp_dir().join(format!("oc2api-health-rt-{}", std::process::id()));
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };

        let recs = vec![
            HealthRecord {
                name: "n1".to_string(),
                healthy: true,
                last_check_ts: 123,
                consecutive_failures: 0,
                last_error: None,
            },
            HealthRecord {
                name: "n2".to_string(),
                healthy: false,
                last_check_ts: 456,
                consecutive_failures: 2,
                last_error: Some("不可达".to_string()),
            },
        ];
        save_records(&recs);
        let loaded = load_records();
        assert_eq!(loaded.len(), 2);
        assert_eq!(loaded[0].name, "n1");
        assert_eq!(loaded[1].consecutive_failures, 2);

        unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") };
        if let Some(v) = orig {
            unsafe { std::env::set_var("OPCODE2API_DATA_DIR", v) };
        }
        std::fs::remove_dir_all(&test_dir).ok();
    }

    /// 回归测试：已停止实例的陈旧健康记录不得计入 total/healthy/unhealthy。
    /// 曾出现「只启动 4 个实例却显示 5 个健康」——records 未按 Running 集合过滤。
    #[test]
    fn test_health_stats_only_count_running() {
        let _guard = crate::config::CONFIG_TEST_LOCK
            .lock()
            .unwrap_or_else(|e| e.into_inner());
        let orig = std::env::var("OPCODE2API_DATA_DIR").ok();
        let test_dir = std::env::temp_dir().join(format!("oc2api-health-{}", std::process::id()));
        unsafe { std::env::set_var("OPCODE2API_DATA_DIR", &test_dir) };

        let (instances_path, binary_dir, runtime_dir) = crate::commands::manager_paths();
        std::fs::create_dir_all(&runtime_dir).ok();
        let mut mgr = crate::instance::InstanceManager::new(
            instances_path,
            binary_dir.clone(),
            runtime_dir.clone(),
        );
        for i in 0..4u16 {
            let name = format!("inst{}", i);
            mgr.add_instance(name.clone(), 28000 + i, format!("node{}", i), "sk".into(), "x:1".into())
                .unwrap();
            if let Some(inst) = mgr.instances.iter_mut().find(|x| x.name == name) {
                inst.status = crate::instance::InstanceStatus::Running;
            }
        }
        mgr.add_instance("inst-stopped".into(), 28010, "nodex".into(), "sk".into(), "x:1".into())
            .unwrap();

        let mut records: Vec<HealthRecord> = (0..4u16)
            .map(|i| HealthRecord {
                name: format!("inst{}", i),
                healthy: true,
                last_check_ts: 1,
                consecutive_failures: 0,
                last_error: None,
            })
            .collect();
        records.push(HealthRecord {
            name: "inst-stopped".into(),
            healthy: true,
            last_check_ts: 1,
            consecutive_failures: 0,
            last_error: None,
        });
        save_records(&records);

        let core = crate::core::AppCore {
            manager: std::sync::Arc::new(std::sync::Mutex::new(mgr)),
            scan: std::sync::Arc::new(crate::probe::ScanController::new()),
            gateway: std::sync::Arc::new(std::sync::Mutex::new(
                crate::gateway::GatewayManager::new(binary_dir, runtime_dir),
            )),
        };

        let summary = run_health_check_once(&core);
        assert_eq!(summary.total, 4, "total 只计当前 Running 实例");
        assert_eq!(
            summary.healthy + summary.unhealthy,
            4,
            "健康+异常之和应等于 Running 实例数"
        );
        assert!(
            !summary.records.iter().any(|r| r.name == "inst-stopped"),
            "已停止实例的陈旧记录不应出现在巡检结果中"
        );

        unsafe { std::env::remove_var("OPCODE2API_DATA_DIR") };
        if let Some(v) = orig {
            unsafe { std::env::set_var("OPCODE2API_DATA_DIR", v) };
        }
        std::fs::remove_dir_all(&test_dir).ok();
    }
}
