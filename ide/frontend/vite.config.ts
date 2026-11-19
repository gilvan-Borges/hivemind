import { defineConfig } from "vite";

export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"]
  }
});
