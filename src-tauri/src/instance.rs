use crate::clash_yaml;
use crate::opencode_cfg;
use crate::singbox;
use anyhow::{bail, Context, Result};
use serde::{Deserialize, Serialize};
use std::fs;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct Instance {
    pub name: String,
    pub port: u16,
    pub node: String,
    #[serde(default)]
    pub password: String,
    #[serde(default)]
    pub ip: String,
    pub singbox_port: u16,
    pub pid: Option<u32>,
    pub singbox_pid: Option<u32>,
    pub status: InstanceStatus,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[derive(Default)]
pub enum InstanceStatus {
    #[default]
    Stopped,
    Starting,
    Running,
    Stopping,
    Error(String),
}


pub struct InstanceManager {
    pub instances: Vec<Instance>,
    pub config_path: PathBuf,
    pub binary_dir: PathBuf,
    pub runtime_dir: PathBuf,
}

impl InstanceManager {
    pub fn new(config_path: PathBuf, binary_dir: PathBuf, runtime_dir: PathBuf) -> Self {
        InstanceManager {
            instances: Vec::new(),
            config_path,
            binary_dir,
            runtime_dir,
        }
    }

    pub fn add_instance(
        &mut self,
        name: String,
        port: u16,
        node: String,
        password: String,
        ip: String,
    ) -> Result<()> {
        if self.instances.iter().any(|i| i.name == name) {
            bail!("实例 '{}' 已存在", name);
        }
        if self.instances.iter().any(|i| i.port == port) {
            bail!("端口 {} 已被其他实例占用", port);
        }
        let instance = Instance {
            name,
            port,
            node,
            password,
            ip,
            singbox_port: port + 10000,
            pid: None,
            singbox_pid: None,
            status: InstanceStatus::Stopped,
        };
        self.instances.push(instance);
        self.save()?;
        Ok(())
    }

    pub fn remove_instance(&mut self, name: &str) -> Result<()> {
        let idx = self
            .instances
            .iter()
            .position(|i| i.name == name)
            .context("实例不存在")?;
        if self.instances[idx].status == InstanceStatus::Running
            || self.instances[idx].status == InstanceStatus::Starting
        {
            bail!("请先停止实例 '{}' 再删除", name);
        }
        self.instances.remove(idx);
        self.save()?;
        Ok(())
    }

    pub fn start_instance(&mut self, name: &str) -> Result<()> {
        let idx = self
            .instances
            .iter()
            .position(|i| i.name == name)
            .context("实例不存在")?;

        if self.instances[idx].status == InstanceStatus::Running {
            bail!("实例 '{}' 已在运行", name);
        }

        let password = self.instances[idx].password.clone();

        // 1. 根据节点名查找 Clash 节点
        let nodes = clash_yaml::list_nodes_with_group()
            .context("无法读取代理节点（本地或外部控制）")?;
        let node = nodes
            .iter()
            .find(|n| n.name == self.instances[idx].node)
            .with_context(|| format!("未找到节点 '{}'", self.instances[idx].node))?;

        let instance_dir = self.runtime_dir.join(&self.instances[idx].name);
        fs::create_dir_all(&instance_dir)
            .context("创建实例目录失败")?;
        let log_dir = instance_dir.join("logs");
        fs::create_dir_all(&log_dir)
            .context("创建日志目录失败")?;

        self.instances[idx].status = InstanceStatus::Starting;
        self.save()?;

        // 2. 生成并启动 sing-box
        let singbox_cfg = singbox::build_singbox_config(node, self.instances[idx].singbox_port)
            .context("生成 sing-box 配置失败")?;
        let singbox_cfg_path = instance_dir.join("singbox.json");
        fs::write(&singbox_cfg_path, singbox_cfg)
            .context("写入 sing-box 配置失败")?;

        let singbox_exe = self.binary_dir.join("sing-box.exe");
        let singbox_bin = if singbox_exe.exists() {
            singbox_exe
        } else {
            let fallback = self.binary_dir.join("sing-box");
            if !fallback.exists() {
                bail!("未找到 sing-box 可执行文件: {}", self.binary_dir.join("sing-box.exe").display());
            }
            fallback
        };

        let singbox_stdout = fs::File::create(log_dir.join("singbox.out.log"))
            .context("创建 sing-box 输出日志失败")?;
        let singbox_stderr = fs::File::create(log_dir.join("singbox.err.log"))
            .context("创建 sing-box 错误日志失败")?;

        let singbox_child = Command::new(&singbox_bin)
            .args(["run", "-c"])
            .arg(&singbox_cfg_path)
            .stdout(Stdio::from(singbox_stdout))
            .stderr(Stdio::from(singbox_stderr))
            .spawn()
            .context("启动 sing-box 失败")?;
        self.instances[idx].singbox_pid = Some(singbox_child.id());

        // 等待 sing-box SOCKS5 端口就绪，再启动 opencode2api
        let singbox_port = self.instances[idx].singbox_port;
        if !wait_for_port(singbox_port, Duration::from_secs(10)) {
            let _ = kill_process(singbox_child.id());
            self.instances[idx].status = InstanceStatus::Error("sing-box 端口未就绪".into());
            self.instances[idx].singbox_pid = None;
            self.save()?;
            bail!("sing-box 在 10s 内未能监听 127.0.0.1:{}", singbox_port);
        }

        // 3. 生成并启动 opencode2api
        let oc_cfg = opencode_cfg::build_opencode_config(self.instances[idx].singbox_port)
            .context("生成 opencode2api 配置失败")?;
        let oc_cfg_path = instance_dir.join("opencode2api.json");
        fs::write(&oc_cfg_path, oc_cfg)
            .context("写入 opencode2api 配置失败")?;

        let oc_bin = self.binary_dir.join("opencode2api.exe");
        let oc_bin = if oc_bin.exists() {
            oc_bin
        } else {
            let fallback = self.binary_dir.join("opencode2api");
            if !fallback.exists() {
                let _ = kill_process(singbox_child.id());
                bail!("未找到 opencode2api 可执行文件: {}", self.binary_dir.join("opencode2api.exe").display());
            }
            fallback
        };

        let oc_stdout = fs::File::create(log_dir.join("opencode2api.out.log"))
            .context("创建 opencode2api 输出日志失败")?;
        let oc_stderr = fs::File::create(log_dir.join("opencode2api.err.log"))
            .context("创建 opencode2api 错误日志失败")?;

        let oc_child = Command::new(&oc_bin)
            .arg("-port")
            .arg(self.instances[idx].port.to_string())
            .arg("-config")
            .arg(&oc_cfg_path)
            .arg("-password")
            .arg(password)
            .stdout(Stdio::from(oc_stdout))
            .stderr(Stdio::from(oc_stderr))
            .spawn()
            .context("启动 opencode2api 失败")?;
        self.instances[idx].pid = Some(oc_child.id());

        let api_port = self.instances[idx].port;
        if !wait_for_port(api_port, Duration::from_secs(15)) {
            let _ = kill_process(oc_child.id());
            let _ = kill_process(singbox_child.id());
            self.instances[idx].status = InstanceStatus::Error("opencode2api 端口未就绪".into());
            self.instances[idx].pid = None;
            self.instances[idx].singbox_pid = None;
            self.save()?;
            bail!("opencode2api 在 15s 内未能监听 0.0.0.0:{}", api_port);
        }

        self.instances[idx].status = InstanceStatus::Running;
        self.save()?;
        Ok(())
    }

    pub fn stop_instance(&mut self, name: &str) -> Result<()> {
        let idx = self
            .instances
            .iter()
            .position(|i| i.name == name)
            .context("实例不存在")?;

        self.instances[idx].status = InstanceStatus::Stopping;
        self.save()?;

        // 先停 opencode2api，再停 sing-box
        if let Some(pid) = self.instances[idx].pid {
            kill_process(pid).ok();
        }
        if let Some(pid) = self.instances[idx].singbox_pid {
            kill_process(pid).ok();
        }

        self.instances[idx].status = InstanceStatus::Stopped;
        self.instances[idx].pid = None;
        self.instances[idx].singbox_pid = None;
        self.save()?;
        Ok(())
    }

    pub fn list_instances(&self) -> &[Instance] {
        &self.instances
    }

    /// 校验实例存在且 Running，返回其 API 端口（供锁外探测）。
    pub fn prepare_test(&self, name: &str) -> Result<u16> {
        let inst = self
            .find_instance(name)
            .with_context(|| format!("实例 '{}' 不存在", name))?;

        if inst.status != InstanceStatus::Running {
            bail!(
                "实例 '{}' 当前状态为 {:?}，请先启动后再测试",
                name,
                inst.status
            );
        }
        Ok(inst.port)
    }

    /// 对运行中的实例请求 `GET /v1/models`，快速验活。
    pub fn test_instance(&self, name: &str) -> Result<TestResult> {
        let port = self.prepare_test(name)?;
        Ok(probe_models(name, port))
    }

    #[allow(dead_code)]
    pub fn find_instance(&self, name: &str) -> Option<&Instance> {
        self.instances.iter().find(|i| i.name == name)
    }

    #[allow(dead_code)]
    pub fn find_instance_mut(&mut self, name: &str) -> Option<&mut Instance> {
        self.instances.iter_mut().find(|i| i.name == name)
    }

    fn save(&self) -> Result<()> {
        let data = serde_json::to_string_pretty(&self.instances)
            .context("序列化实例失败")?;
        fs::write(&self.config_path, data)
            .context("写入实例文件失败")?;
        Ok(())
    }

    pub fn load(&mut self) -> Result<()> {
        if self.config_path.exists() {
            let data = fs::read_to_string(&self.config_path)
                .context("读取实例文件失败")?;
            self.instances = serde_json::from_str(&data)
                .context("解析实例文件失败")?;
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TestResult {
    pub name: String,
    pub port: u16,
    pub ok: bool,
    pub status_code: Option<u16>,
    pub model_count: Option<usize>,
    pub message: String,
    pub latency_ms: u64,
}

/// 对指定端口探测 `GET /v1/models`（可在锁外 / spawn_blocking 中调用）。
pub fn probe_models(name: &str, port: u16) -> TestResult {
    let start = Instant::now();
    match http_get_json(port, "/v1/models", Duration::from_secs(10)) {
        Ok((status, body)) => {
            let latency_ms = start.elapsed().as_millis() as u64;
            if !(200..300).contains(&status) {
                return TestResult {
                    name: name.to_string(),
                    port,
                    ok: false,
                    status_code: Some(status),
                    model_count: None,
                    message: format!("HTTP {}，响应: {}", status, truncate(&body, 200)),
                    latency_ms,
                };
            }
            let model_count = count_models_in_body(&body);
            TestResult {
                name: name.to_string(),
                port,
                ok: true,
                status_code: Some(status),
                model_count,
                message: match model_count {
                    Some(n) => format!("models 正常，共 {} 个模型", n),
                    None => "models 接口返回成功（未能解析模型数量）".to_string(),
                },
                latency_ms,
            }
        }
        Err(e) => {
            let latency_ms = start.elapsed().as_millis() as u64;
            TestResult {
                name: name.to_string(),
                port,
                ok: false,
                status_code: None,
                model_count: None,
                message: format!("请求失败: {}", e),
                latency_ms,
            }
        }
    }
}

fn truncate(s: &str, max: usize) -> String {
    let mut t: String = s.chars().take(max).collect();
    if s.chars().count() > max {
        t.push('…');
    }
    t
}

fn count_models_in_body(body: &str) -> Option<usize> {
    let v: serde_json::Value = serde_json::from_str(body).ok()?;
    if let Some(arr) = v.get("data").and_then(|d| d.as_array()) {
        return Some(arr.len());
    }
    if let Some(arr) = v.as_array() {
        return Some(arr.len());
    }
    None
}

/// 向本机实例发简单 HTTP/1.1 GET，返回 (status, body)。
pub(crate) fn http_get_json(port: u16, path: &str, timeout: Duration) -> Result<(u16, String)> {
    let addr = format!("127.0.0.1:{}", port);
    let mut stream =
        TcpStream::connect(&addr).with_context(|| format!("无法连接 {}", addr))?;
    stream
        .set_read_timeout(Some(timeout))
        .context("设置读超时失败")?;
    stream
        .set_write_timeout(Some(Duration::from_secs(5)))
        .context("设置写超时失败")?;

    let req = format!(
        "GET {path} HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nConnection: close\r\nAccept: application/json\r\nUser-Agent: opencode2api-manager/0.1\r\n\r\n"
    );
    stream
        .write_all(req.as_bytes())
        .context("发送 HTTP 请求失败")?;

    let mut buf = Vec::new();
    stream
        .read_to_end(&mut buf)
        .context("读取 HTTP 响应失败")?;
    let raw = String::from_utf8_lossy(&buf);
    let (header, body) = raw
        .split_once("\r\n\r\n")
        .or_else(|| raw.split_once("\n\n"))
        .unwrap_or((raw.as_ref(), ""));

    let status = header
        .lines()
        .next()
        .and_then(|line| {
            // HTTP/1.1 200 OK
            line.split_whitespace().nth(1)?.parse::<u16>().ok()
        })
        .unwrap_or(0);

    Ok((status, body.trim().to_string()))
}

/// 等待本地 TCP 端口可连接
pub(crate) fn wait_for_port(port: u16, timeout: Duration) -> bool {
    let addr = format!("127.0.0.1:{}", port);
    let start = Instant::now();
    while start.elapsed() < timeout {
        if TcpStream::connect(&addr).is_ok() {
            return true;
        }
        thread::sleep(Duration::from_millis(200));
    }
    false
}

/// 按 PID 终止进程（Windows 用 taskkill，其他平台用 sysinfo）
pub fn kill_process(pid: u32) -> Result<()> {
    #[cfg(windows)]
    {
        let output = Command::new("taskkill")
            .args(["/PID", &pid.to_string(), "/F"])
            .output()
            .context("执行 taskkill 失败")?;
        if output.status.success() {
            Ok(())
        } else {
            bail!("终止进程 {} 失败: {}", pid, String::from_utf8_lossy(&output.stderr));
        }
    }
    #[cfg(not(windows))]
    {
        use sysinfo::{Pid, System};
        let mut sys = System::new_all();
        sys.refresh_processes();
        if let Some(p) = sys.process(Pid::from_u32(pid)) {
            p.kill();
            Ok(())
        } else {
            bail!("进程 {} 不存在", pid);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::env;

    fn temp_dir(name: &str) -> PathBuf {
        let dir = env::temp_dir().join(format!("opencode2api-manager-test-{}", name));
        fs::create_dir_all(&dir).ok();
        dir
    }

    fn new_manager(name: &str) -> InstanceManager {
        let dir = temp_dir(name);
        InstanceManager::new(
            dir.join("instances.json"),
            dir.join("bin"),
            dir.join("runtime"),
        )
    }

    #[test]
    fn test_add_instance() {
        let mut manager = new_manager("add");
        manager
            .add_instance("user1".to_string(), 8088, "新加坡 G1".to_string(), "".to_string(), "".to_string())
            .unwrap();
        assert_eq!(manager.instances.len(), 1);
        assert_eq!(manager.instances[0].name, "user1");
        assert_eq!(manager.instances[0].port, 8088);
        assert_eq!(manager.instances[0].singbox_port, 18088);
        fs::remove_dir_all(temp_dir("add")).ok();
    }

    #[test]
    fn test_add_duplicate() {
        let mut manager = new_manager("dup");
        manager.add_instance("a".to_string(), 8088, "n".to_string(), "".to_string(), "".to_string()).unwrap();
        let r = manager.add_instance("a".to_string(), 8089, "n".to_string(), "".to_string(), "".to_string());
        assert!(r.is_err());
        fs::remove_dir_all(temp_dir("dup")).ok();
    }

    #[test]
    fn test_add_duplicate_port() {
        let mut manager = new_manager("dupport");
        manager.add_instance("a".to_string(), 8088, "n".to_string(), "".to_string(), "".to_string()).unwrap();
        let r = manager.add_instance("b".to_string(), 8088, "n".to_string(), "".to_string(), "".to_string());
        assert!(r.is_err());
        fs::remove_dir_all(temp_dir("dupport")).ok();
    }

    #[test]
    fn test_start_not_found() {
        let mut manager = new_manager("startnf");
        let r = manager.start_instance("nobody");
        assert!(r.is_err());
        fs::remove_dir_all(temp_dir("startnf")).ok();
    }
}