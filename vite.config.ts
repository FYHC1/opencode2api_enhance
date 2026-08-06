import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  clearScreen: false,
  base: './',
  server: {
    // headless 开发：浏览器访问 vite dev server 时 /api 转发到本地管理服务
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:19090',
        changeOrigin: true,
      },
    },
  },
})
