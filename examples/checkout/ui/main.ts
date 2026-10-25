import { App } from "@modelcontextprotocol/ext-apps";
import { createApps } from "holon-ui";

const view = new App({ name: "checkout-view", version: "1.0.0" });
await view.connect();

// The view calls its own server through the MCP Apps host, which owns the connection.
const apps = createApps({ checkout: { host: view } });

document.querySelector("button")!.addEventListener("click", async () => {
  const id = document.querySelector<HTMLInputElement>("#order")!.value;
  const { total } = await apps.checkout.tools.total({ id });
  document.querySelector("output")!.textContent = String(total);
});
