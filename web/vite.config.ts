import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 产物直接进 internal/httpapi/webdist，由 go:embed 收进单二进制。
export default defineConfig({
  plugins: [react()],
  server: {
    // 本地开发时代理到网关，管理 API 无需 CORS 配置。
    proxy: {
      '/admin': 'http://127.0.0.1:8080',
      '/healthz': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: '../internal/httpapi/webdist',
    emptyOutDir: true,
  },
})
