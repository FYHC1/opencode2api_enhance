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
    /// Go core 管理器子进程（大步3：管理职责已移交 HTTP，壳负责拉起/随退出终止）
    pub core_child: Mutex<Option<std::process::Child>>,
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
    let data_dir = instances_path
        .parent()
        .map(|p| p.to_path_buf())
        .unwrap_or_default();
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

    // 大步3：管理职责移交 Go core（HTTP /api/admin/*）。壳只负责：
    // 释放内嵌二进制 → 以管理器方式拉起 core → 窗口承载 core 的 SPA。
    unsafe {
        std::env::set_var("OPCODE2API_DATA_DIR", &data_dir);
    }
    let core_child = match spawn_core_manager(&data_dir) {
        Ok(child) => Some(child),
        Err(e) => {
            eprintln!("启动 core 管理器失败: {e}");
            None
        }
    };

    tauri::Builder::default()
        .manage(AppState {
            manager,
            scan: Arc::new(probe::ScanController::new()),
            gateway: gateway_manager,
            core_child: Mutex::new(core_child),
        })
        .invoke_handler(tauri::generate_handler![
            // 大步3：仅保留壳命令（窗口/托盘/自启/二进制）；管理命令已被 HTTP 取代
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
                    // 先停 core 管理器（连带其网关/实例），再退出（ExitRequested 也会兜底清理）
                    if let Some(state) = app.try_state::<AppState>() {
                        if let Ok(mut ch) = state.core_child.lock() {
                            if let Some(mut c) = ch.take() {
                                let _ = c.kill();
                            }
                        }
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

            // 窗口承载 core 管理器 SPA（core 已在本机 18100 端口就绪）
            if let Some(w) = app.get_webview_window("main") {
                let _ = w.navigate(
                    tauri::Url::parse("http://127.0.0.1:18100/").expect("core manager url"),
                );
            }
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
                    // 先停 core 管理器，再停网关、实例（端口全部释放）
                    if let Ok(mut ch) = state.core_child.lock() {
                        if let Some(mut c) = ch.take() {
                            let _ = c.kill();
                        }
                    }
                    if let Ok(mut gateway) = state.gateway.lock() {
                        gateway.stop();
                    }
                    commands::stop_all_instances(&state);
                }
            }
        });
}

/// 拉起 Go core 管理器（bin/opencode2api.exe -port 18100 ...），等待 /health 就绪。
/// 数据目录经 OPCODE2API_DATA_DIR 注入（setup 已设置）。
fn spawn_core_manager(data_dir: &std::path::Path) -> std::io::Result<std::process::Child> {
    use std::io::{Read, Write};
    use std::process::Command;
    use std::time::Duration;

    let (_, binary_dir, _) = commands::manager_paths();
    let exe = binary_dir.join("opencode2api.exe");
    if !exe.exists() {
        return Err(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            "bin/opencode2api.exe 不存在（未释放内嵌组件）",
        ));
    }
    let cfg_path = data_dir.join("config.json");
    let mut cmd = Command::new(&exe);
    cmd.args(["-port", "18100", "-password", "sk-unified-local", "-config"])
        .arg(&cfg_path)
        .arg("-log-level")
        .arg("warn");
    // 隐藏 core 子进程的控制台窗口（与 instance.rs no_window 一致）
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
    }
    let child = cmd.spawn()?;

    // 等待 /health 就绪（最多 ~15s）
    let mut ready = false;
    for _ in 0..30 {
        std::thread::sleep(Duration::from_millis(500));
        if let Ok(mut stream) = std::net::TcpStream::connect("127.0.0.1:18100") {
            let _ = stream.set_read_timeout(Some(Duration::from_secs(1)));
            let _ = stream.write_all(b"GET /health HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n");
            let mut buf = [0u8; 64];
            if stream.read(&mut buf).is_ok() && String::from_utf8_lossy(&buf).contains("200") {
                ready = true;
                break;
            }
        }
    }
    if !ready {
        eprintln!("警告: core 管理器 /health 未在预期时间内就绪（窗口可能先于服务加载）");
    }
    Ok(child)
}
