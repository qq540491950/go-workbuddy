import path from "node:path"
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Visual-regression stub harness: serves the real app UI with a canned IPC
// transport in a plain browser. Run with:
//   npx vite --config vite.stub.ts --port 9246
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: 9246,
    strictPort: true,
  },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
