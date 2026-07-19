import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In dev, Vite serves the SPA on :5173 and proxies the backend paths to
// `fragments serve` (default :8088) so the browser sees a single origin — cookies
// and SSE work exactly as they will in production (embedded build). Override the
// target with FRAGMENTS_DEV_BACKEND if you run the server on another port.
const backend = process.env.FRAGMENTS_DEV_BACKEND || 'http://localhost:8088'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': { target: backend, changeOrigin: true },
      '/thumbs': { target: backend, changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
