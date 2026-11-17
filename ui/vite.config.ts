import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // Relative asset paths, so the app works under any path prefix
  // (Home Assistant ingress serves add-ons under one).
  base: './',
  build: { outDir: 'dist', emptyOutDir: true },
  // `pnpm dev` talks to a running `unified serve`.
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
