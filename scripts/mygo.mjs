import { delimiter, join } from "node:path";

if (process.platform === "win32" && process.env.SystemRoot) {
  // ponytail: prioritize Windows cmd over npm's `cmd` shim; remove when PATH is fixed globally.
  process.env.PATH = `${join(process.env.SystemRoot, "System32")}${delimiter}${process.env.PATH ?? ""}`;
}

await import("../node_modules/mygo-cli/bin/mygo.js");
