pub mod clash_yaml;
pub mod commands;
pub mod config;
pub mod embed;
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
}

/// 桌面入口：释放内嵌二进制 → 构建 AppState → 启动 Tauri（托盘常驻）
pub fn run() {
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

    let (instances_path, _, runtime_dir) = commands::manager_paths();
    let mut manager = instance::InstanceManager::new(instances_path, binary_dir, runtime_dir);
    let _ = manager.load();

    tauri::Builder::default()
        .manage(AppState {
            manager: Arc::new(Mutex::new(manager)),
            scan: Arc::new(probe::ScanController::new()),
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_nodes,
            commands::list_instances,
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
            commands::hide_to_tray,
            commands::toggle_maximize,
            commands::quit_app
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
                    // 先停全部实例，再退出（与 before-quit 同理）
                    if let Some(state) = app.try_state::<AppState>() {
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
            // 应用退出（托盘"退出"/quit）时停止全部运行中的实例
            if let tauri::RunEvent::ExitRequested { .. } = event {
                if let Some(state) = app.try_state::<AppState>() {
                    commands::stop_all_instances(&state);
                }
            }
        });
}
