import { apps } from "holon-ui";

// The view calls its own server through the MCP Apps host that embeds it; the host owns the connection.
document.querySelector("button")!.addEventListener("click", async () => {
  const id = document.querySelector<HTMLInputElement>("#order")!.value;
  const { total } = await apps.checkout.tools.total({ id });
  document.querySelector("output")!.textContent = String(total);
});
