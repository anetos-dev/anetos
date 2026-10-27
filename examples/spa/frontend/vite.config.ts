import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The example's single-page app, in React. In development, open Vite's
// address, http://localhost:5173 (anetos dev runs Vite): it serves the
// app with hot reload, and forwards /api to the app, at anetos dev's
// address (HTTP_ADDR, 127.0.0.1:8080). `vite build` writes it into
// public/spa, which the binary embeds and serves at / (vite.SPA in
// main.go).

// Go's //go:embed leaves out files whose names start with _ or .: the
// build's files are named without them.
const embeddable = (name: string) => name.replace(/^[_.]+/, '')

export default defineConfig({
  plugins: [react()],
  // region: server
  server: {
    port: 5173,
    strictPort: true, // HTTP_SPA_ORIGINS and AUTH_CLIENT_URL in .env name it
    proxy: { '/api': 'http://127.0.0.1:8080' },
  },
  // endregion
  build: {
    outDir: '../public/spa',
    emptyOutDir: true,
    rolldownOptions: {
      output: {
        entryFileNames: (chunk) => `assets/${embeddable(chunk.name)}-[hash].js`,
        chunkFileNames: (chunk) => `assets/${embeddable(chunk.name)}-[hash].js`,
        assetFileNames: (asset) => `assets/${embeddable(asset.names[0] ?? 'asset').replace(/\.[^.]*$/, '')}-[hash][extname]`,
      },
    },
  },
})
