import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const apiTarget = process.env.UPIMG_API_TARGET || 'http://127.0.0.1:17788';

export default defineConfig({
  base: './',
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
        ws: true,
        configure(proxy) {
          proxy.on('proxyReqWs', (proxyReq) => {
            proxyReq.setHeader('origin', apiTarget);
          });
        },
      },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: false,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
});
