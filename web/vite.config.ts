import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Geliştirmede (npm run dev) API isteklerini Go sunucusuna yönlendirir;
// üretimde aynı işi nginx yapar (nginx.conf).
const api = process.env.API_URL ?? 'http://localhost:8080';

const proxy = { '/api': api, '/auth': api, '/health': api, '/metrics': api, '/openapi.yaml': api };

export default defineConfig({
  plugins: [react()],
  server: { port: 3000, proxy },
  preview: { port: 3000, proxy },
  build: {
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
});
