#!/usr/bin/env node
process.argv.splice(2, 0, "init", "--lang", "ts");
await import("holon-types/bin/holon.js");
