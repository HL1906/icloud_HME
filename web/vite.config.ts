import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8081',
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
    // 测试中的时间断言(如 AppShell 的「登录有效期至 10-23 09:40」)按东八区硬编码,
    // 组件用本地时区格式化。固定测试进程时区,保证本地与 CI(UTC 环境)结果一致。
    env: {
      TZ: 'Asia/Shanghai',
    },
  },
})
