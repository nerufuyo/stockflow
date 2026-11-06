import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    // Same-origin in dev and in the nginx image, so the backend needs no CORS.
    proxy: { '/api': 'http://localhost:8080' },
  },
})
