#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    let args: Vec<String> = std::env::args().collect();
    if args.len() > 1 && args[1] == "serve" {
        headless_main(&args);
    } else {
        opencode2api::run()
    }
}

/// headless 模式：仅启动 HTTP 服务（无窗口），默认 127.0.0.1:19090。
/// 支持 `serve --port <n>` 覆盖端口（亦可经 OPCODE2API_HTTP_PORT 环境变量）；
/// `serve --bind <addr>` 覆盖监听地址（默认仅回环；公网/局域网访问需显式
/// `--bind 0.0.0.0`，此时管理 API 无鉴权，请务必配合防火墙/反代限制来源）。
fn headless_main(args: &[String]) {
    use opencode2api::core::AppCore;
    use opencode2api::server;
    use std::sync::Arc;

    let port = args
        .iter()
        .position(|a| a == "--port")
        .and_then(|i| args.get(i + 1))
        .and_then(|p| p.parse::<u16>().ok())
        .or_else(|| {
            std::env::var("OPCODE2API_HTTP_PORT")
                .ok()
                .and_then(|p| p.parse().ok())
        })
        .unwrap_or(19090);
    let bind = args
        .iter()
        .position(|a| a == "--bind")
        .and_then(|i| args.get(i + 1))
        .cloned()
        .unwrap_or_else(|| "127.0.0.1".to_string());

    let core = Arc::new(AppCore::new());
    let rt = tokio::runtime::Runtime::new().expect("无法创建运行时");
    // 后台健康巡检（headless 模式同样生效；配置间隔为 0 时内部自动休眠）
    {
        let core_for_health = core.clone();
        rt.spawn(async move {
            opencode2api::health::health_loop(core_for_health).await;
        });
    }
    // 后台订阅自动拉取（headless 模式同样生效）
    {
        let core_for_sub = core.clone();
        rt.spawn(async move {
            opencode2api::subscribe::subscribe_loop(core_for_sub).await;
        });
    }
    if let Err(e) = rt.block_on(server::serve(&format!("{}:{}", bind, port), core)) {
        eprintln!("Headless 服务启动失败: {}", e);
        std::process::exit(1);
    }
}
