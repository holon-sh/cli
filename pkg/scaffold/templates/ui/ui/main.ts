import { connect } from "#holon-ui";

const form = document.querySelector<HTMLFormElement>("#greet")!;
const name = document.querySelector<HTMLInputElement>("#name")!;
const greeting = document.querySelector<HTMLOutputElement>("#greeting")!;

const app = await connect();

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const { result } = await app.tools.greet({ name: name.value });
  greeting.value = result;
});

// In an MCP Apps host, show the greeting the agent asked for.
const opened = await app.opened;
if (opened?.tool === "greet") greeting.value = opened.output.result;
