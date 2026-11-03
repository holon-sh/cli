// src/view.ts
import { readFile } from "node:fs/promises";
var VIEW_MIME_TYPE = "text/html;profile=mcp-app";
function appView(name, uri, file, options = {}) {
  const csp = { connectDomains: [], resourceDomains: [], ...options.csp };
  const page = readFile(file, "utf8");
  page.catch(() => {
  });
  return [
    name,
    uri,
    { mimeType: VIEW_MIME_TYPE, _meta: { ui: { csp } } },
    async () => ({
      ttlMs: 0,
      cacheScope: "private",
      contents: [{ uri, mimeType: VIEW_MIME_TYPE, text: await page }]
    })
  ];
}
export {
  VIEW_MIME_TYPE,
  appView
};
