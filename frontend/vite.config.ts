import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the API is proxied so the browser talks to a single origin,
// mirroring the nginx setup used in the container.
const apiTarget = process.env.VITE_API_PROXY ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    proxy: {
      "/api": apiTarget,
      "/health": apiTarget,
    },
  },
});
