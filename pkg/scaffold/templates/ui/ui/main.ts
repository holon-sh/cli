import { App, createApps } from "#holon-ui";

const form = document.querySelector<HTMLFormElement>("#greet")!;
const name = document.querySelector<HTMLInputElement>("#name")!;
const greeting = document.querySelector<HTMLOutputElement>("#greeting")!;

const view = import.meta.env.MODE === "mcp" ? new App({ name: "{{.App}}", version: "0.1.0" }) : undefined;
await view?.connect();
const server = view ?? createApps({ "{{.Key}}": { endpoint: new URL("/mcp/{{.App}}", location.origin) } })["{{.Key}}"];

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const { result } = await server.tools.greet({ name: name.value });
  greeting.value = result;
});
