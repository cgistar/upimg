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
    // Mermaid 的图表运行时代码按需加载，压缩后单块接近 1MB 属于预期范围。
    chunkSizeWarningLimit: 1000,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name][extname]',
      },
    },
  },
});
