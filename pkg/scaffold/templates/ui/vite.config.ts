import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

// The view builds twice: dist/web is the page served at /{{.App}}/, and dist/mcp/index.html is the
// single file the server returns as its MCP App resource.
export default defineConfig(({ mode }) => ({
  root: "ui",
  base: mode === "mcp" ? "./" : "/{{.App}}/",
  plugins: mode === "mcp" ? [viteSingleFile()] : [],
  build: { outDir: mode === "mcp" ? "../dist/mcp" : "../dist/web", emptyOutDir: true },
}));
