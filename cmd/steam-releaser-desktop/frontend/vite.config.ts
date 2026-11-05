import { execFileSync } from "node:child_process";
import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

let commitSha = "";
try {
  commitSha = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: fileURLToPath(new URL(".", import.meta.url)),
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
    timeout: 1000,
  })
    .trim()
    .slice(0, 7);
} catch {
  // Source archives can be built without Git metadata.
}

export default defineConfig({
  base: "./",
  define: {
    "import.meta.env.VITE_COMMIT_SHA": JSON.stringify(commitSha),
  },
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
