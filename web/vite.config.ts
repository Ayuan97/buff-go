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
  },
})
