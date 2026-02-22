import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import tailwindcss from "@tailwindcss/vite";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";

// MIME types needed for backend-served static assets.
const MIME = {
  ".avif": "image/avif",
  ".webp": "image/webp",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".png": "image/png",
  ".gif": "image/gif",
  ".mp4": "video/mp4",
  ".webm": "video/webm",
  ".mov": "video/quicktime",
};

/**
 * Vite plugin: serve static directories directly from the filesystem
 * instead of proxying through Node's http-proxy.  This eliminates the
 * extra loopback TCP round-trip and brings latency down to a simple
 * fs.createReadStream call (~1 ms instead of ~400 ms).
 *
 * @param {Record<string, string>} map  { '/url-prefix': '/abs/fs/path' }
 */
function serveStatic(map) {
  return {
    name: "vite-serve-static",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        // Strip query string.
        const url = req.url.split("?")[0];

        for (const [prefix, dir] of Object.entries(map)) {
          if (!url.startsWith(prefix)) continue;

          const rel = url.slice(prefix.length);
          // Prevent directory traversal.
          const abs = path.resolve(dir, "." + rel);
          if (!abs.startsWith(path.resolve(dir))) {
            res.writeHead(403);
            res.end();
            return;
          }

          let stat;
          try {
            stat = fs.statSync(abs);
          } catch {
            // File not found — fall through to proxy / 404.
            break;
          }

          if (!stat.isFile()) break;

          const etag = `"${stat.mtime.getTime().toString(16)}-${stat.size.toString(16)}"`;
          if (req.headers["if-none-match"] === etag) {
            res.writeHead(304);
            res.end();
            return;
          }

          const ext = path.extname(abs).toLowerCase();
          const mime = MIME[ext] ?? "application/octet-stream";
          res.writeHead(200, {
            "Content-Type": mime,
            "Content-Length": stat.size,
            ETag: etag,
            "Cache-Control": "public, max-age=31536000, immutable",
          });

          if (req.method === "HEAD") {
            res.end();
            return;
          }

          fs.createReadStream(abs).pipe(res);
          return;
        }

        next();
      });
    },
  };
}

// Absolute path to the backend's public directory (same container).
const BACKEND_PUBLIC = path.resolve(__dirname, "../backend/public");

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    serveStatic({
      "/images": path.join(BACKEND_PUBLIC, "images"),
      "/videos": path.join(BACKEND_PUBLIC, "videos"),
      "/upload": path.join(BACKEND_PUBLIC, "upload"),
    }),
  ],

  server: {
    port: 5173,
    host: "0.0.0.0", // bind all interfaces so VS Code port forwarding reaches the Windows host
    proxy: {
      "/api": {
        target: "http://localhost:3000",
        changeOrigin: true,
      },
      // /images, /videos, /upload are now served directly from disk
      // by the serveStatic plugin above — no proxy needed.
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
