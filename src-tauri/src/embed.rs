use anyhow::{Context, Result};
use std::fs;
use std::path::Path;

pub const OPENCODE2API: &[u8] = include_bytes!("../../bin/opencode2api.exe");
pub const SINGBOX: &[u8] = include_bytes!("../../bin/sing-box.exe");

/// 确保 bin 目录下的两个子程序存在且与内嵌版本一致。
/// 返回是否发生了写入（True 表示首次释放或更新）。
pub fn ensure_binaries(bin_dir: &Path) -> Result<bool> {
    fs::create_dir_all(bin_dir).with_context(|| {
        format!("创建目录失败: {}", bin_dir.display())
    })?;
    let wrote_oc = ensure_file(bin_dir, "opencode2api.exe", OPENCODE2API)?;
    let wrote_sb = ensure_file(bin_dir, "sing-box.exe", SINGBOX)?;
    Ok(wrote_oc || wrote_sb)
}

fn ensure_file(dir: &Path, name: &str, data: &[u8]) -> Result<bool> {
    let path = dir.join(name);
    // 内容级校验：仅比大小可能因不同构建恰好同长而漏更新（导致旧版残留）。
    // 比较关键区段哈希（取首/中/尾三段的 FNV-1a），避免整文件读取大对象。
    let need_write = match fs::read(&path) {
        Ok(existing) => !same_content(&existing, data),
        Err(_) => true,
    };
    if need_write {
        fs::write(&path, data)
            .with_context(|| format!("写入 {} 失败: {}", name, path.display()))?;
        Ok(true)
    } else {
        Ok(false)
    }
}

/// 分段 FNV-1a 哈希比较（首 1KB / 中 1KB / 尾 1KB，外加长度）。
/// 足够区分不同构建产物；不读全量大文件。
fn same_content(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    hash_sample(a) == hash_sample(b)
}

fn hash_sample(d: &[u8]) -> u64 {
    let n = d.len();
    let mut h: u64 = 0xcbf29ce484222325;
    for s in [0usize, n / 2, n.saturating_sub(1024)] {
        let end = (s + 1024).min(n);
        if s >= n {
            continue;
        }
        for &b in &d[s..end] {
            h ^= b as u64;
            h = h.wrapping_mul(0x100000001b3);
        }
    }
    h
}
