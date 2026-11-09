import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export default defineConfig(({ mode }) => ({
  root: "ui",
  base: mode === "mcp" ? "./" : "/{{.App}}/",
  plugins: mode === "mcp" ? [viteSingleFile()] : [],
  build: { outDir: mode === "mcp" ? "../dist/mcp" : "../dist/web", emptyOutDir: true },
}));
