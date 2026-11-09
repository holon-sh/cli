import { appClass, bindApps, type Identity } from "holon-ui/runtime";

const form = document.querySelector<HTMLFormElement>("#greet")!;
const name = document.querySelector<HTMLInputElement>("#name")!;
const greeting = document.querySelector<HTMLOutputElement>("#greeting")!;

const identity: Identity = { name: "{{.Name}}", version: "0.1.0" };
const contracts = { "{{.Key}}": null };

const view = import.meta.env.MODE === "mcp" ? new (appClass(identity, contracts, "{{.Key}}"))({ name: "{{.App}}", version: "0.1.0" }) : undefined;
await view?.connect();
const server = view ?? bindApps(identity, contracts, { "{{.Key}}": { endpoint: new URL("/mcp/{{.App}}", location.origin) } })["{{.Key}}"];

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const { structuredContent } = await server.callTool({ name: "greet", arguments: { name: name.value } });
  greeting.value = (structuredContent as { result: string }).result;
});
