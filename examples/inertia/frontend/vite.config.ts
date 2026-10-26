import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// The example's frontend: `vite build` writes it into public/build, which
// the app embeds and serves under /build/; anetos dev runs Vite and
// forwards /build/ to it.

// Go's //go:embed leaves out files whose names start with _ or .: the
// build's files are named without them.
const embeddable = (name: string) => name.replace(/^[_.]+/, '')

export default defineConfig({
  base: '/build/',
  plugins: [react()],
  build: {
    outDir: '../public/build',
    emptyOutDir: true,
    manifest: 'manifest.json',
    rolldownOptions: {
      input: ['src/app.tsx'],
      output: {
        entryFileNames: (chunk) => `assets/${embeddable(chunk.name)}-[hash].js`,
        chunkFileNames: (chunk) => `assets/${embeddable(chunk.name)}-[hash].js`,
        assetFileNames: (asset) => `assets/${embeddable(asset.names[0] ?? 'asset').replace(/\.[^.]*$/, '')}-[hash][extname]`,
      },
    },
  },
})
