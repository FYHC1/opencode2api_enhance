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
    let need_write = match fs::metadata(&path) {
        Ok(m) => m.len() != data.len() as u64,
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
