import { defaultClientConditions, defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export default defineConfig(({ mode }) => ({
  root: "ui",
  base: mode === "mcp" ? "./" : "/{{.App}}/",
  plugins: mode === "mcp" ? [viteSingleFile()] : [],
  resolve: { conditions: mode === "mcp" ? ["mcp-app", ...defaultClientConditions] : undefined },
  build: { outDir: mode === "mcp" ? "../dist/mcp" : "../dist/web", emptyOutDir: true },
}));
