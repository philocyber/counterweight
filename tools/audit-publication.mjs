#!/usr/bin/env node
// Inspect the prospective Git tree without adding files to the index.
// Optional Gitleaks scans use a source snapshot and all reachable Git refs.
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const flags = new Set(process.argv.slice(2));
if ([...flags].some((f) => !["--secrets", "--history"].includes(f))) {
  console.error("Usage: node tools/audit-publication.mjs [--secrets] [--history]");
  process.exit(2);
}
const git = (...args) => execFileSync("git", args, { cwd: root, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 });
const files = [...new Set(git("ls-files", "-c", "-o", "--exclude-standard", "-z").split("\0").filter(Boolean))];
const failures = new Set();
const candidates = [];
const fixture = "skills/api-recon/scripts/test_fixtures/tls/key.pem";
const fixtureHash = "aa89b24e68ad1fb55a6c66415cb59eae1167d46fc54a409af1c9b7933caf84aa";
const privateHome = (line, file) => {
  const paths = [...line.matchAll(/(?:[A-Za-z]:[/\\](?:Users|Documents and Settings)[/\\]|\/(?:home|Users)\/)([^\s/\\]+)[/\\]/g)];
  // The paste, path-redaction and scanner tests explicitly use these generic example usernames.
  const examples = new Set(["web/src/lib/chat-paste.test.mjs", "report/enrich_test.go", "report/sarif_test.go", "scanner/scanner_test.go", "server/scan_test.go", "agent/change_review_context_test.go", "report/pr_summary_test.go", "server/codereview_pr_test.go"]);
  return paths.some((m) => !(examples.has(file) && ["user", "me"].includes(m[1])));
};
const internalIdentity = (line) => /\b(?:scan|workspace)[ _-]?(?:id)?\s*[:=]?\s*[0-9a-f]{8}-[0-9a-f-]{27,}\b/i.test(line);
for (const file of files) {
  const path = join(root, file);
  if (!existsSync(path)) continue; // Deleted files will not be published.
  if (/(^|\/)(node_modules|\.next|__pycache__)(\/|$)/.test(file) || /^(web\/out|server\/webui\/dist|dist)\//.test(file)) {
    const parts = file.split("/");
    const index = parts.findIndex((p) => ["node_modules", ".next", "__pycache__", "out", "dist"].includes(p));
    failures.add(`Generated/dependency directory is eligible for Git: ${parts.slice(0, index + 1).join("/")}/`);
    continue;
  }
  const stat = lstatSync(path);
  if (!stat.isFile()) {
    failures.add(`Review non-regular file before publishing: ${file}`);
    continue;
  }
  candidates.push(file);
  if (/^(package(-lock)?\.json)$/.test(file)) failures.add(`Local security-scan tooling is eligible for Git: ${file}`);
  if (/^(data|transcripts|bench)\//.test(file) || (/(^|\/)\.env(?:\..*)?$/.test(file) && file !== ".env.example") || /(^|\/)(config\.json|jwt\.key)$/.test(file) && file !== ".impeccable/config.json") {
    failures.add(`Runtime/private file is eligible for Git: ${file}`);
  }
  if (file !== fixture && /\.(key|p12|pfx|db|db-wal|db-shm|sqlite|sqlite3|dump|bak|log|jsonl|har|pcap|pcapng)$/i.test(file)) {
    failures.add(`Review sensitive data/key file: ${file}`);
  }
  const raw = readFileSync(path);
  if (file === fixture && createHash("sha256").update(raw).digest("hex") !== fixtureHash) failures.add(`Public TLS fixture changed; re-review the key: ${file}`);
  if (raw.includes(0)) continue;
  const text = raw.toString("utf8");
  // Report locations only. Never print source lines that might contain secrets.
  text.split(/\r?\n/).forEach((line, i) => {
    if (privateHome(line, file)) {
      failures.add(`Machine-specific home path: ${file}:${i + 1}`);
    }
    if (internalIdentity(line)) failures.add(`Internal scan/workspace identity: ${file}:${i + 1}`);
  });
}
console.log(`Inspected ${candidates.length} prospective source files (tracked and non-ignored untracked).`);
for (const failure of failures) console.error(failure);
let failed = failures.size > 0;
const gitleaks = process.env.GITLEAKS_BIN || "gitleaks";
if (flags.has("--secrets")) {
  const snapshot = mkdtempSync(join(tmpdir(), "counterweight-source-audit-"));
  for (const file of candidates) {
    const dest = join(snapshot, file);
    mkdirSync(dirname(dest), { recursive: true });
    copyFileSync(join(root, file), dest);
  }
  const result = spawnSync(gitleaks, ["dir", ".", "--redact", "--no-banner", "--no-color"], { cwd: snapshot, stdio: "inherit" });
  if (result.error) console.error(`Gitleaks could not start: ${result.error.code}`);
  console.log(`Source snapshot retained outside the repository: ${snapshot}`);
  failed ||= result.status !== 0;
}
if (flags.has("--history")) {
  let commit = "", file = "";
  const historyFindings = new Set();
  for (const line of git("log", "--all", "-p", "--no-ext-diff", "--no-textconv", "--format=COMMIT:%H").split(/\r?\n/)) {
    if (line.startsWith("COMMIT:")) commit = line.slice(7);
    if (line.startsWith("+++ b/")) file = line.slice(6);
    if (!line.startsWith("+") || line.startsWith("+++")) continue;
    if (privateHome(line, file)) historyFindings.add(`Historical machine home path: ${commit.slice(0, 12)}:${file}`);
    if (internalIdentity(line)) historyFindings.add(`Historical internal scan/workspace identity: ${commit.slice(0, 12)}:${file}`);
  }
  for (const finding of historyFindings) console.error(finding);
  failed ||= historyFindings.size > 0;
  const result = spawnSync(gitleaks, ["git", ".", "--log-opts=--all", "--redact", "--no-banner", "--no-color"], { cwd: root, stdio: "inherit" });
  if (result.error) console.error(`Gitleaks could not start: ${result.error.code}`);
  failed ||= result.status !== 0;
}
console.log(failed ? "Publication audit has unresolved findings." : "Publication audit passed for the requested checks.");
process.exitCode = failed ? 1 : 0;
