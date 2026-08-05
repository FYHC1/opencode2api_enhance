pub mod call_log;
pub mod clash_yaml;
pub mod commands;
pub mod config;
pub mod embed;
pub mod gateway;
pub mod instance;
pub mod opencode_cfg;
pub mod probe;
pub mod singbox;

use std::sync::{Arc, Mutex};
use tauri::Manager;

/// 全局共享状态（与 Windsurf Account Manager 的 AppState 模式一致）
pub struct AppState {
    pub manager: Arc<Mutex<instance::InstanceManager>>,
    pub scan: Arc<probe::ScanController>,
    pub gateway: Arc<Mutex<gateway::GatewayManager>>,
}


/// 桌面入口：释放内嵌二进制 → 构建 AppState → 启动 Tauri（托盘常驻）
pub fn run() {
    // 调试构建默认隔离数据目录：与正式版（%APPDATA%\opencode2api-manager）
    // 分开，避免实例池/配置/runtime 互相干扰。可用 OPCODE2API_DATA_DIR 显式覆盖。
    // 注意：环境变量存在但为空串时视为未设置（否则会静默回落共享生产目录）。
    if cfg!(debug_assertions) {
        let unset_or_empty = match std::env::var_os("OPCODE2API_DATA_DIR") {
            None => true,
            Some(v) => v.is_empty(),
        };
        if unset_or_empty {
            let base = dirs::config_dir().unwrap_or_else(|| std::path::PathBuf::from("."));
            // 单线程启动阶段设置环境变量，安全
            unsafe {
                std::env::set_var(
                    "OPCODE2API_DATA_DIR",
                    base.join("opencode2api-manager-dev"),
                );
            }
        }
    }
    // 调试构建默认开启 SSE 流信息输出（tauri dev 终端实时显示收发流，排查 IDE 解析问题）；
    // 正式版（release）不受影响。可用 OPCODE2API_SSE_DEBUG=0 关闭。
    if cfg!(debug_assertions) && std::env::var("OPCODE2API_SSE_DEBUG").is_err() {
        unsafe {
            std::env::set_var("OPCODE2API_SSE_DEBUG", "1");
        }
    }
    // 启动前释放内嵌子程序到 exe 旁 bin/ 目录
    let (_, binary_dir, _) = commands::manager_paths();
    match embed::ensure_binaries(&binary_dir) {
        Ok(wrote) => {
            if wrote {
                println!("已释放内置组件到 {}", binary_dir.display());
            }
        }
        Err(e) => eprintln!("警告: 释放内置组件失败: {}", e),
    }

    let (instances_path, binary_dir, runtime_dir) = commands::manager_paths();
    let mut manager = instance::InstanceManager::new(
        instances_path,
        binary_dir.clone(),
        runtime_dir.clone(),
    );
    let _ = manager.load();
    // 启动即校正：上次非正常退出留下的"Running 但进程已死"状态修正为 Stopped
    let _ = manager.reconcile_states();

    let manager = Arc::new(Mutex::new(manager));
    let gateway_manager = Arc::new(Mutex::new(gateway::GatewayManager::new(
        binary_dir,
        runtime_dir,
    )));
    // 启动即同步统一网关：恢复上次「运行中且入池」实例的代理池（无入池实例则停网关）
    if let (Ok(mgr), Ok(mut gateway)) = (manager.lock(), gateway_manager.lock()) {
        let _ = gateway.sync(mgr.list_instances());
    }

    tauri::Builder::default()
        .manage(AppState {
            manager,
            scan: Arc::new(probe::ScanController::new()),
            gateway: gateway_manager,
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_nodes,
            commands::list_instances,
            commands::refresh_states,
            commands::add_instance,
            commands::remove_instance,
            commands::start_instance,
            commands::stop_instance,
            commands::test_instance,
            commands::batch_add,
            commands::batch_start,
            commands::batch_stop,
            commands::batch_delete,
            commands::port_suggest,
            commands::port_check,
            commands::scan_start,
            commands::scan_status,
            commands::scan_stop,
            commands::config_get,
            commands::config_set,
            commands::autostart_get,
            commands::autostart_set,
            commands::get_binaries_info,

            commands::get_stats,
            commands::get_call_log,
            commands::hide_to_tray,
            commands::toggle_maximize,
            commands::quit_app,
            commands::gateway_status,
            commands::gateway_set_route_mode,
            commands::gateway_stop,
            commands::set_join_gateway
        ])
.setup(|app| {
            use tauri::Manager;

            // 托盘菜单：右键显示「显示主窗口 / 退出」
            let show_i =
                tauri::menu::MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
            let quit_i =
                tauri::menu::MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = tauri::menu::Menu::with_items(app, &[&show_i, &quit_i])?;

// 图标：取 built-in 窗口图标（由 tauri.conf bundle.icon 提供，打包后必有）
            let tray_icon = app.default_window_icon().cloned();

            let mut tray = tauri::tray::TrayIconBuilder::with_id("main-tray")
                .tooltip("opencode2api 管理器")
                .menu(&menu)
                .show_menu_on_left_click(true); // 左键单击也显示菜单（含退出），便于发现
            if let Some(icon) = tray_icon {
                tray = tray.icon(icon);
            }

            tray.on_menu_event(|app, event| match event.id().as_ref() {
                "show" => {
                    if let Some(w) = app.get_webview_window("main") {
                        let _ = w.show();
                        let _ = w.unminimize();
                        let _ = w.set_focus();
                    }
                }
                "quit" => {
                    // 先停网关 + 全部实例，再退出（ExitRequested 也会兜底清理）
                    if let Some(state) = app.try_state::<AppState>() {
                        if let Ok(mut gateway) = state.gateway.lock() {
                            gateway.stop();
                        }
                        commands::stop_all_instances(&state);
                    }
                    app.exit(0);
                }
                _ => {}
            })
            .on_tray_icon_event(|tray, event| {
                // 左键单击显示窗口；右键由系统自动弹菜单（无需手动处理）
                if let tauri::tray::TrayIconEvent::Click {
button: tauri::tray::MouseButton::Left,
                    ..
                } = event
                {
                    let app = tray.app_handle();
                    if let Some(w) = app.get_webview_window("main") {
                        let _ = w.show();
                        let _ = w.unminimize();
                        let _ = w.set_focus();
                    }
                }
            })
            .build(app)?;
            Ok(())
        })
        .on_window_event(|window, event| {
            // 关闭窗口 = 最小化到托盘（实例继续运行）
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .build(tauri::generate_context!("tauri.conf.json"))
        .expect("桌面构建失败")
        .run(|app, event| {
            // 应用退出（托盘"退出"/quit）时停止全部运行中的实例 + 统一网关，
            // 确保网关进程和实例进程不残留后台（网关端口、实例端口全部释放）
            if let tauri::RunEvent::ExitRequested { .. } = event {
                if let Some(state) = app.try_state::<AppState>() {
                    // 先停网关（释放网关端口），再停实例（释放实例端口）
                    if let Ok(mut gateway) = state.gateway.lock() {
                        gateway.stop();
                    }
                    commands::stop_all_instances(&state);
                }
            }
        });
}
