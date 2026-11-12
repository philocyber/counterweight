// docs/runpod.md is the source of truth for the in-app RunPod guide.
// Run from any directory: node web/scripts/sync-runpod-docs.mjs
import { readFileSync, writeFileSync } from "node:fs";

const readme = readFileSync(new URL("../../docs/runpod.md", import.meta.url), "utf8");
const guide = readme.split("<!-- runpod-guide:start -->\n")[1]?.split("\n<!-- runpod-guide:end -->")[0];
if (!guide) throw new Error("docs/runpod.md RunPod guide markers are missing");
writeFileSync(new URL("../src/app/(main)/docs/_runpod.json", import.meta.url), `${JSON.stringify(guide, null, 2)}\n`);
