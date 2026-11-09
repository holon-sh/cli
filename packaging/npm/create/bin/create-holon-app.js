#!/usr/bin/env node
process.argv.splice(2, 0, "init");
await import("holon-types/bin/holon.js");
