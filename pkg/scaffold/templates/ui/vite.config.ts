import { defaultClientConditions, defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export default defineConfig({
  root: "ui",
  plugins: [viteSingleFile()],
  build: { outDir: "../dist/web", emptyOutDir: true },
  environments: { mcp: { consumer: "client", resolve: { conditions: ["mcp-app", ...defaultClientConditions] }, build: { outDir: "../dist/mcp" } } },
});
