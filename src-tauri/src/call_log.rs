//! 全流程调用日志读取：解析 Go 网关进程落盘的 call_log.jsonl。
//! 文件位于 runtime/_unified-gateway/call_log.jsonl（与 stats.json 同目录）。

use serde::{Deserialize, Serialize};
use std::collections::VecDeque;
use std::path::Path;

/// 单条事件（连接/超时/切换/完成等）
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CallLogEvent {
    #[serde(rename = "type")]
    pub event_type: String,
    #[serde(default)]
    pub node: String,
    #[serde(default)]
    pub detail: String,
    #[serde(default)]
    pub at: Option<String>,
}

/// 单条调用记录（一个请求一行 JSONL）
/// 注意：Go 端字段为 snake_case（req_id/prompt_tokens 等），
/// 此处不使用 rename_all，保持与 Go JSON 字段名一致。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CallLogRecord {
    pub req_id: String,
    pub ts: String,
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub model: String,
    #[serde(default)]
    pub stream: bool,
    #[serde(default)]
    pub route_mode: String,
    #[serde(default)]
    pub nodes: Vec<String>,
    #[serde(default)]
    pub events: Vec<CallLogEvent>,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub prompt_tokens: i64,
    #[serde(default)]
    pub completion_tokens: i64,
    #[serde(default)]
    pub duration_ms: i64,
    #[serde(default)]
    pub err_msg: String,
}

impl CallLogRecord {
    /// 状态前缀：前端按 【成功】/【失败】 过滤与着色
    pub fn status_text(&self) -> &'static str {
        if self.status == "ok" {
            "【成功】"
        } else {
            "【失败】"
        }
    }

    /// 是否有切换/异常事件（前端"只看失败"用它）
    pub fn has_issue(&self) -> bool {
        if self.status != "ok" {
            return true;
        }
        self.events.iter().any(|e| {
            matches!(
                e.event_type.as_str(),
                "switch"
                    | "ttft_timeout"
                    | "silence_timeout"
                    | "stream_interrupt"
                    | "stream_error"
                    | "connect_error"
                    | "upstream_error"
                    | "all_failed"
            )
        })
    }
}

/// 读取 call_log.jsonl，返回最新 N 条（按文件顺序，最后的是最新）。
/// 文件不存在或解析失败返回空列表（不报错，网关未启用日志时前端显示空）。
pub fn read_call_log(path: &Path, max: usize) -> Vec<CallLogRecord> {
    let data = match std::fs::read(path) {
        Ok(d) => d,
        Err(_) => return Vec::new(),
    };
    let mut records: VecDeque<CallLogRecord> = VecDeque::new();
    for line in data.split(|&b| b == b'\n') {
        if line.is_empty() {
            continue;
        }
        if let Ok(rec) = serde_json::from_slice::<CallLogRecord>(line) {
            if records.len() >= max {
                records.pop_front();
            }
            records.push_back(rec);
        }
    }
    records.into_iter().collect()
}

/// 过滤参数（前端日志页）。
/// 注意：现有 CallLogRecord 无 instance/message 字段，
/// 此处按实际字段（nodes/model/path/err_msg/req_id/ts/status）调整。
#[derive(Debug, Default, Clone, serde::Deserialize)]
pub struct CallLogFilter {
    /// 节点名过滤：nodes 数组中任一元素包含该串即匹配
    pub node: Option<String>,
    /// 关键词：匹配 model / path / err_msg / req_id
    pub keyword: Option<String>,
    /// ok = 无异常；error = 有异常/切换（has_issue）；其他值不过滤
    pub status: Option<String>,
    pub limit: Option<usize>,
    pub offset: Option<usize>,
    /// 起始时间（ISO 字符串，字典序即时间序）
    pub from_ts: Option<String>,
    /// 结束时间（ISO 字符串）
    pub to_ts: Option<String>,
}

impl CallLogFilter {
    fn matches(&self, r: &CallLogRecord) -> bool {
        if let Some(node) = &self.node {
            if !r.nodes.iter().any(|n| n.contains(node)) {
                return false;
            }
        }
        if let Some(kw) = &self.keyword {
            let hay = format!("{} {} {} {}", r.model, r.path, r.err_msg, r.req_id);
            if !hay.contains(kw) {
                return false;
            }
        }
        if let Some(s) = &self.status {
            match s.as_str() {
                "ok" => {
                    if r.has_issue() {
                        return false;
                    }
                }
                "error" => {
                    if !r.has_issue() {
                        return false;
                    }
                }
                _ => {}
            }
        }
        if let Some(t) = &self.from_ts {
            if r.ts < *t {
                return false;
            }
        }
        if let Some(t) = &self.to_ts {
            if r.ts > *t {
                return false;
            }
        }
        true
    }
}

/// 按过滤条件读取日志（读全量后过滤 + 分页，返回最新在前）。
pub fn read_call_log_filtered(path: &Path, filter: &CallLogFilter) -> Vec<CallLogRecord> {
    let all = read_call_log(path, usize::MAX);
    let mut out: Vec<CallLogRecord> = all.into_iter().filter(|r| filter.matches(r)).collect();
    out.reverse(); // 最新在前
    let offset = filter.offset.unwrap_or(0).min(out.len());
    let limit = filter.limit.unwrap_or(100).min(out.len() - offset);
    out.drain(offset..).take(limit).collect()
}

/// 日志聚合条目（按节点组合统计）
#[derive(Debug, Clone, serde::Serialize)]
pub struct CallLogAggregate {
    /// 节点组合（nodes.join(" → ")，无节点为 "-"）
    pub instance: String,
    pub total: usize,
    pub errors: usize,
    /// 最近一次时间（ISO 字符串）
    pub last_ts: String,
}

/// 日志聚合（按节点组合统计条数/错误数/最近时间）。
pub fn call_log_aggregate(path: &Path) -> Vec<CallLogAggregate> {
    let all = read_call_log(path, usize::MAX);
    let mut map: std::collections::BTreeMap<String, (usize, usize, String)> = Default::default();
    for r in &all {
        let key = if r.nodes.is_empty() {
            "-".to_string()
        } else {
            r.nodes.join(" → ")
        };
        let e = map.entry(key).or_insert((0, 0, String::new()));
        e.0 += 1;
        if r.has_issue() {
            e.1 += 1;
        }
        if r.ts > e.2 {
            e.2 = r.ts.clone();
        }
    }
    map.into_iter()
        .map(|(instance, (total, errors, last_ts))| CallLogAggregate {
            instance,
            total,
            errors,
            last_ts,
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    #[test]
    fn test_read_call_log_parses_records() {
        let dir = std::env::temp_dir().join(format!("calllog_test_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("call_log.jsonl");
        let mut f = std::fs::File::create(&path).unwrap();
        writeln!(
            f,
            r#"{{"req_id":"r1","ts":"2026-08-05T10:00:00+08:00","path":"/v1/chat/completions","model":"m","stream":true,"route_mode":"failover","nodes":["127.0.0.1:28100"],"events":[{{"type":"connect_ok","node":"127.0.0.1:28100","detail":"connected"}}],"status":"ok","prompt_tokens":1,"completion_tokens":2,"duration_ms":100}}"#
        )
        .unwrap();
        writeln!(
            f,
            r#"{{"req_id":"r2","ts":"2026-08-05T10:01:00+08:00","model":"m","status":"fail","err_msg":"boom","events":[{{"type":"ttft_timeout","node":"n1","detail":"no first token"}}]}}"#
        )
        .unwrap();
        f.flush().unwrap();

        let recs = read_call_log(&path, 10);
        assert_eq!(recs.len(), 2, "should parse 2 records");
        assert_eq!(recs[0].req_id, "r1");
        assert_eq!(recs[0].status_text(), "【成功】");
        assert!(!recs[0].has_issue());
        assert_eq!(recs[1].req_id, "r2");
        assert_eq!(recs[1].status_text(), "【失败】");
        assert!(recs[1].has_issue());
        assert_eq!(recs[1].events[0].event_type, "ttft_timeout");

        // 环形截断
        let capped = read_call_log(&path, 1);
        assert_eq!(capped.len(), 1);
        assert_eq!(capped[0].req_id, "r2");

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn test_read_call_log_missing_file() {
        let recs = read_call_log(Path::new("C:/nonexistent/call_log.jsonl"), 10);
        assert!(recs.is_empty());
    }

    fn test_path(tag: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("calllog_test_{}_{}", std::process::id(), tag));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("call_log.jsonl");
        let mut f = std::fs::File::create(&path).unwrap();
        writeln!(
            f,
            r#"{{"req_id":"r1","ts":"2026-08-05T10:00:00+08:00","model":"gpt-4o","path":"/v1/chat/completions","status":"ok","nodes":["n1"],"events":[{{"type":"connect_ok","node":"n1"}}]}}"#
        )
        .unwrap();
        writeln!(
            f,
            r#"{{"req_id":"r2","ts":"2026-08-05T10:01:00+08:00","model":"gpt-4o-mini","path":"/v1/chat/completions","status":"fail","err_msg":"boom","nodes":["n1","n2"],"events":[{{"type":"switch","node":"n2"}}]}}"#
        )
        .unwrap();
        writeln!(
            f,
            r#"{{"req_id":"r3","ts":"2026-08-05T10:02:00+08:00","model":"claude-3","path":"/v1/messages","status":"ok","nodes":["n3"]}}"#
        )
        .unwrap();
        f.flush().unwrap();
        path
    }

    #[test]
    fn test_read_call_log_filtered_by_keyword() {
        let path = test_path("kw");
        let f = CallLogFilter {
            keyword: Some("boom".into()),
            ..Default::default()
        };
        let recs = read_call_log_filtered(&path, &f);
        assert_eq!(recs.len(), 1);
        assert_eq!(recs[0].req_id, "r2");

        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }

    #[test]
    fn test_read_call_log_filtered_by_node() {
        let path = test_path("node");
        let f = CallLogFilter {
            node: Some("n1".into()),
            limit: Some(10),
            ..Default::default()
        };
        let recs = read_call_log_filtered(&path, &f);
        assert_eq!(recs.len(), 2);
        assert_eq!(recs[0].req_id, "r2", "最新在前");

        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }

    #[test]
    fn test_read_call_log_filtered_by_status_and_paging() {
        let path = test_path("status");
        let f = CallLogFilter {
            status: Some("error".into()),
            limit: Some(1),
            offset: Some(0),
            ..Default::default()
        };
        let recs = read_call_log_filtered(&path, &f);
        assert_eq!(recs.len(), 1);
        assert_eq!(recs[0].req_id, "r2");

        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }

    #[test]
    fn test_read_call_log_filtered_by_ts_range() {
        let path = test_path("ts");
        let f = CallLogFilter {
            from_ts: Some("2026-08-05T10:01:00+08:00".into()),
            to_ts: Some("2026-08-05T10:02:00+08:00".into()),
            ..Default::default()
        };
        let recs = read_call_log_filtered(&path, &f);
        assert_eq!(recs.len(), 2);
        assert_eq!(recs[0].req_id, "r3");

        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }

    #[test]
    fn test_call_log_aggregate() {
        let path = test_path("agg");
        let agg = call_log_aggregate(&path);
        assert_eq!(agg.len(), 3);
        let r2 = agg.iter().find(|a| a.instance == "n1 → n2").unwrap();
        assert_eq!(r2.total, 1);
        assert_eq!(r2.errors, 1);
        let r1 = agg.iter().find(|a| a.instance == "n1").unwrap();
        assert_eq!(r1.total, 1);
        assert_eq!(r1.errors, 0);
        assert!(r1.last_ts < r2.last_ts);

        std::fs::remove_dir_all(path.parent().unwrap()).ok();
    }
}
