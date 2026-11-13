import { connect } from "holon-ui/runtime";

const form = document.querySelector<HTMLFormElement>("#greet")!;
const name = document.querySelector<HTMLInputElement>("#name")!;
const greeting = document.querySelector<HTMLOutputElement>("#greeting")!;

const app = await connect({ identity: { name: "{{.Name}}", version: "0.1.0" }, contracts: { "{{.Key}}": null }, own: "{{.Key}}", endpoint: "/mcp/{{.App}}" });

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const { structuredContent } = await app.callTool({ name: "greet", arguments: { name: name.value } });
  greeting.value = (structuredContent as { result: string }).result;
});
