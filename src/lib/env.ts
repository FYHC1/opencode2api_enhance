// 运行形态判定：桌面壳（Tauri WebView：Windows/Linux/macOS 桌面 exe） vs headless（浏览器直跑：Web 版 / Docker / Linux 服务器）
// Tauri 2 注入 window.__TAURI_INTERNALS__，Tauri 1 注入 window.__TAURI__；普通浏览器两者皆无。
export const isDesktop =
  typeof window !== 'undefined' &&
  ('__TAURI_INTERNALS__' in window || '__TAURI__' in window)
