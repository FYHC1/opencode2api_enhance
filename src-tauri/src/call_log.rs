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
                "switch" | "ttft_timeout" | "silence_timeout" | "stream_interrupt"
                    | "stream_error" | "connect_error" | "upstream_error" | "all_failed"
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
}
