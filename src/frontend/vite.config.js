import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],

  server: {
    port: 5173,
    host: "0.0.0.0", // bind all interfaces so VS Code port forwarding reaches the Windows host
    proxy: {
      "/api": {
        target: "http://localhost:3000",
        changeOrigin: true,
      },
      "/images": {
        target: "http://localhost:3000",
        changeOrigin: true,
      },
      "/videos": {
        target: "http://localhost:3000",
        changeOrigin: true,
      },
    },
  },

  build: {
    // target modern browsers — smaller output, no legacy polyfills
    target: "es2022",
    // produce smaller CSS by inlining below this threshold
    cssCodeSplit: true,
    rollupOptions: {
      output: {
        // keep router and state in stable chunks for long-term caching;
        // react + react-dom are already inlined by Vite (React 19 ESM)
        manualChunks: {
          router: ["react-router-dom"],
          state: ["zustand"],
        },
      },
    },
  },
});
