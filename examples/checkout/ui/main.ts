import { App } from "holon-ui";

// The view calls its own server through the MCP Apps host that embeds it; the host owns the connection.
const view = new App({ name: "checkout-view", version: "1.0.0" });
await view.connect();

document.querySelector("button")!.addEventListener("click", async () => {
  const id = document.querySelector<HTMLInputElement>("#order")!.value;
  const { total } = await view.tools.total({ id });
  document.querySelector("output")!.textContent = String(total);
});
