//! 纯逻辑核心：与 Tauri 完全解耦，桌面/headless 共用。
//! 持有实例管理器、扫描控制器、统一网关管理器。
//!
//! 架构：`AppCore` 是全部业务逻辑的单一持有者。
//! Tauri command 层与 axum HTTP 层都只通过 `Arc<AppCore>` 访问，
//! 因此两套入口天然共享同一份实例池 / 网关 / 扫描状态。

use crate::commands;
use crate::embed;
use crate::gateway::GatewayManager;
use crate::instance::InstanceManager;
use crate::probe::ScanController;
use std::sync::{Arc, Mutex};

/// 全局共享状态（纯 Rust 类型，无 tauri 依赖）
pub struct AppCore {
    pub manager: Arc<Mutex<InstanceManager>>,
    pub scan: Arc<ScanController>,
    pub gateway: Arc<Mutex<GatewayManager>>,
}

impl AppCore {
    /// 构建核心：释放内嵌二进制 → 加载实例 → 校正僵尸状态 → 同步统一网关。
    pub fn new() -> Self {
        let (_, binary_dir, _) = commands::manager_paths();
        match embed::ensure_binaries(&binary_dir) {
            Ok(wrote) => {
                if wrote {
                    println!("已释放内置组件到 {}", binary_dir.display());
                }
            }
            Err(e) => eprintln!("警告: 释放内置组件失败: {}", e),
        }

        let manager = Arc::new(Mutex::new(commands::create_manager()));
        let gateway_manager = Arc::new(Mutex::new(GatewayManager::new(
            commands::manager_paths().1,
            commands::manager_paths().2,
        )));
        // 启动即同步统一网关：恢复上次「运行中且入池」实例的代理池（无入池实例则停网关）
        if let (Ok(mgr), Ok(mut gateway)) = (manager.lock(), gateway_manager.lock()) {
            let _ = gateway.sync(mgr.list_instances());
        }

        AppCore {
            manager,
            scan: Arc::new(ScanController::new()),
            gateway: gateway_manager,
        }
    }
}

impl Default for AppCore {
    fn default() -> Self {
        Self::new()
    }
}
