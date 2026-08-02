#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod clash_yaml;
mod config;
mod embed;
mod instance;
mod opencode_cfg;
mod probe;
mod singbox;
mod web;

use clap::{Parser, Subcommand};
use config::Config;
use instance::InstanceManager;
use std::path::PathBuf;

#[derive(Parser)]
#[command(name = "opencode2api-manager")]
#[command(about = "Multi-instance proxy manager for opencode2api")]
#[command(version)]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    /// 启动 Web 管理界面
    Serve {
        /// 监听地址，默认 127.0.0.1:9099
        #[arg(long, default_value = "127.0.0.1:9099")]
        bind: String,
    },

    /// Manage instances
    Instance {
        #[command(subcommand)]
        action: InstanceAction,
    },

    /// Manage configuration
    Config {
        #[command(subcommand)]
        action: ConfigAction,
    },

    /// List available proxy nodes from Clash
    Node {
        #[command(subcommand)]
        action: NodeAction,
    },
}

#[derive(Subcommand)]
enum InstanceAction {
    /// Add a new instance
    Add {
        #[arg(long)]
        name: String,

        #[arg(long)]
        port: u16,

        #[arg(long)]
        node: String,
    },

    /// Start an instance
    Start {
        #[arg(long)]
        name: String,
    },

    /// Stop an instance
    Stop {
        #[arg(long)]
        name: String,
    },

    /// Remove a stopped instance
    Remove {
        #[arg(long)]
        name: String,
    },

    /// Test a running instance via GET /v1/models
    Test {
        #[arg(long)]
        name: String,
    },

    /// List all instances
    List,
}

#[derive(Subcommand)]
enum ConfigAction {
    /// Set a configuration value
    Set {
        key: String,
        value: String,
    },

    /// Get a configuration value
    Get {
        key: String,
    },
}

#[derive(Subcommand)]
enum NodeAction {
    /// List available proxy nodes
    List,

    /// 串行扫描节点可用性（GET /v1/models）
    Scan {
        /// 只扫指定节点名（可多次）；默认扫全部含凭据节点
        #[arg(long)]
        node: Vec<String>,

        /// 探针 API 端口
        #[arg(long, default_value_t = probe::DEFAULT_PROBE_API_PORT)]
        api_port: u16,

        /// 探针 sing-box SOCKS 端口
        #[arg(long, default_value_t = probe::DEFAULT_PROBE_SOCKS_PORT)]
        socks_port: u16,

        /// 单节点超时秒数
        #[arg(long, default_value_t = 12)]
        timeout: u64,
    },
}

fn binary_dir() -> PathBuf {
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|d| d.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from("."))
        .join("bin")
}

fn make_manager() -> InstanceManager {
    let config_dir = config::Config::config_dir();
    let instances_path = config_dir.join("instances.json");
    let runtime_dir = config_dir.join("runtime");
    let mut manager = InstanceManager::new(instances_path, binary_dir(), runtime_dir);
    manager.load().ok();
    manager
}

fn main() {
    match Cli::try_parse() {
        Ok(cli) => {
            let rt = tokio::runtime::Runtime::new().expect("创建运行时失败");
            rt.block_on(cli_main(cli));
        }
        Err(e) => {
            if std::env::args().count() > 1 {
                let _ = e.print();
                std::process::exit(1);
            }
            desktop_main();
        }
    }
}

/// 桌面模式：后台启动管理服务 + 打开 Tauri 窗口（关闭 = 最小化到托盘）
fn desktop_main() {
    let rt = tokio::runtime::Runtime::new().expect("创建运行时失败");
    let addr = rt.block_on(async {
        let _ = embed::ensure_binaries(&binary_dir());
        web::spawn_server("127.0.0.1:9099").await
    });
    if let Ok(a) = addr {
        println!("管理界面: http://{}/", a);
    }
    std::mem::forget(rt);

    use tauri::Manager;

    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            hide_to_tray,
            toggle_maximize,
            quit_app
        ])
        .setup(|app| {
            let show_i = tauri::menu::MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
            let quit_i = tauri::menu::MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = tauri::menu::Menu::with_items(app, &[&show_i, &quit_i])?;
            let tray_icon = app.default_window_icon().cloned();
            let mut tray = tauri::tray::TrayIconBuilder::with_id("main-tray")
                .tooltip("opencode2api-manager")
                .menu(&menu)
                .show_menu_on_left_click(false);
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
                    "quit" => app.exit(0),
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let tauri::tray::TrayIconEvent::Click { .. } = event {
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
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!("tauri.conf.json"))
        .expect("桌面启动失败");
}

/// 收起到托盘（前端交通灯按钮调用）
#[tauri::command]
fn hide_to_tray(app: tauri::AppHandle) {
    use tauri::Manager;
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.hide();
    }
}

/// 退出进程（前端红点确认后调用）
#[tauri::command]
fn quit_app(app: tauri::AppHandle) {
    app.exit(0);
}

/// 最大化/还原（前端交通灯按钮调用）
#[tauri::command]
fn toggle_maximize(app: tauri::AppHandle) {
    use tauri::Manager;
    if let Some(w) = app.get_webview_window("main") {
        if let Ok(max) = w.is_maximized() {
            if max {
                let _ = w.unmaximize();
            } else {
                let _ = w.maximize();
            }
        }
    }
}

async fn cli_main(cli: Cli) {
    if let Commands::Serve { .. } = cli.command {
        match embed::ensure_binaries(&binary_dir()) {
            Ok(wrote) => {
                if wrote {
                    println!("已释放内置组件到 {}", binary_dir().display());
                }
            }
            Err(e) => eprintln!("警告: 释放内置组件失败: {}", e),
        }
    }

    match cli.command {
        Commands::Serve { bind } => {
            if let Err(e) = web::run_server(&bind).await {
                eprintln!("服务启动失败: {}", e);
                std::process::exit(1);
            }
        }
        Commands::Instance { action } => {
            let mut manager = make_manager();

            match action {
                InstanceAction::Add { name, port, node } => {
                    manager.add_instance(name.clone(), port, node.clone()).unwrap_or_else(|e| {
                        eprintln!("错误: {}", e);
                        std::process::exit(1);
                    });
                    println!("已添加实例 '{}'，端口 {}，节点: {}", name, port, node);
                }
                InstanceAction::Start { name } => {
                    let config = Config::load().unwrap_or_default();
                    let password = if config.default_password.is_empty() {
                        "123456".to_string()
                    } else {
                        config.default_password.clone()
                    };
                    manager.start_instance(&name, &password).unwrap_or_else(|e| {
                        eprintln!("错误: {}", e);
                        std::process::exit(1);
                    });
                    println!("实例 '{}' 已启动", name);
                }
                InstanceAction::Stop { name } => {
                    manager.stop_instance(&name).unwrap_or_else(|e| {
                        eprintln!("错误: {}", e);
                        std::process::exit(1);
                    });
                    println!("实例 '{}' 已停止", name);
                }
                InstanceAction::Remove { name } => {
                    manager.remove_instance(&name).unwrap_or_else(|e| {
                        eprintln!("错误: {}", e);
                        std::process::exit(1);
                    });
                    println!("实例 '{}' 已删除", name);
                }
                InstanceAction::Test { name } => {
                    match manager.test_instance(&name) {
                        Ok(result) => {
                            if result.ok {
                                println!(
                                    "实例 '{}' 测试通过: {} ({}ms)",
                                    name, result.message, result.latency_ms
                                );
                            } else {
                                eprintln!(
                                    "实例 '{}' 测试失败: {} ({}ms)",
                                    name, result.message, result.latency_ms
                                );
                                std::process::exit(1);
                            }
                        }
                        Err(e) => {
                            eprintln!("错误: {}", e);
                            std::process::exit(1);
                        }
                    }
                }
                InstanceAction::List => {
                    let instances = manager.list_instances();
                    if instances.is_empty() {
                        println!("暂无实例");
                    } else {
                        for inst in instances {
                            println!(
                                "  {}: 端口={}, 节点={}, sing-box={}, 状态={:?}",
                                inst.name, inst.port, inst.node, inst.singbox_port, inst.status
                            );
                        }
                    }
                }
            }
        }
        Commands::Config { action } => {
            let mut config = Config::load().unwrap_or_default();
            match action {
                ConfigAction::Set { key, value } => {
                    config.set(&key, &value).expect("Failed to set config");
                    println!("Set {} = {}", key, value);
                }
                ConfigAction::Get { key } => match config.get(&key) {
                    Some(val) => println!("{} = {}", key, val),
                    None => println!("{}: not set", key),
                },
            }
        }
        Commands::Node { action } => match action {
            NodeAction::List => match clash_yaml::list_local_nodes() {
                Ok(nodes) => {
                    if nodes.is_empty() {
                        println!("未找到本地代理节点（请检查 Clash Verge profiles 目录）");
                    } else {
                        println!("本地代理节点（共 {} 个）:", nodes.len());
                        for n in &nodes {
                            let cred = if n.password.is_some() || n.uuid.is_some() {
                                "✓"
                            } else {
                                "✗"
                            };
                            println!(
                                "  [{}] {} ({}, {}:{})",
                                cred, n.name, n.node_type, n.server, n.port
                            );
                        }
                    }
                }
                Err(e) => {
                    eprintln!("解析节点失败: {}", e);
                }
            },
            NodeAction::Scan {
                node,
                api_port,
                socks_port,
                timeout,
            } => {
                let config = Config::load().unwrap_or_default();
                let password = if config.default_password.is_empty() {
                    "123456".to_string()
                } else {
                    config.default_password.clone()
                };
                let config_dir = config::Config::config_dir();
                let runtime_dir = config_dir.join("runtime");
                let filter = if node.is_empty() { None } else { Some(node) };

                println!(
                    "开始节点扫描（探针 API:{} SOCKS:{} 超时:{}s）…",
                    api_port, socks_port, timeout
                );
                match probe::scan_nodes_sync(
                    binary_dir(),
                    runtime_dir,
                    password,
                    api_port,
                    socks_port,
                    filter,
                    timeout,
                    |p| {
                        if let Some(ref n) = p.current_node {
                            eprint!("\r[{}/{}] 正在测: {:<40}", p.current, p.total, n);
                            let _ = std::io::Write::flush(&mut std::io::stderr());
                        }
                    },
                ) {
                    Ok(results) => {
                        eprintln!();
                        let ok_n = results.iter().filter(|r| r.ok).count();
                        println!("扫描完成：可用 {}/{}", ok_n, results.len());
                        println!(
                            "{:<4} {:<28} {:<8} {:<10} {:>6}ms  {}",
                            "OK", "节点", "类型", "分类", "延迟", "说明"
                        );
                        for r in &results {
                            println!(
                                "{:<4} {:<28} {:<8} {:<10} {:>6}  {}",
                                if r.ok { "✓" } else { "✗" },
                                truncate_cli(&r.node, 28),
                                r.node_type,
                                r.category,
                                r.latency_ms,
                                r.message
                            );
                        }
                        if ok_n == 0 {
                            std::process::exit(2);
                        }
                    }
                    Err(e) => {
                        eprintln!("扫描失败: {}", e);
                        std::process::exit(1);
                    }
                }
            }
        },
    }
}

fn truncate_cli(s: &str, max: usize) -> String {
    let mut t: String = s.chars().take(max).collect();
    if s.chars().count() > max {
        t.push('…');
    }
    t
}
