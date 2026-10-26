import { defineConfig } from 'vite'

// The build goes straight into the Go package that embeds it (yastationd/internal/webui).
// `pnpm dev` proxies the API to a speaker: YASTATION=http://<speaker> pnpm dev
export default defineConfig({
  esbuild: { jsx: 'automatic', jsxImportSource: 'preact' },
  build: {
    outDir: '../yastationd/internal/webui/dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    target: 'es2020',
  },
  server: {
    proxy: process.env.YASTATION
      ? { '/api': process.env.YASTATION, '/metrics': process.env.YASTATION }
      : undefined,
  },
})
