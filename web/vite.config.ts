import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物输出到 internal/webui/dist，由 Go 进程嵌入提供（Goal 8B）
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // 本地开发把 /api 转给已在跑的控制面进程。控制面要求 Host 与 Origin
    // 精确等于它自己的监听地址，所以这里必须一并改写，否则请求被安全策略拒绝。
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
        headers: { Origin: 'http://127.0.0.1:8080' },
      },
    },
  },
})
