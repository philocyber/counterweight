import { en, es } from "../i18n/messages/execution.ts";
import { familyLabel, hasModeInfo, isCommand, modeOption, selectedMode, shortDigest } from "./execution-modes.ts";
import assert from "node:assert/strict";
import test from "node:test";

const caps = { files_confined: true, processes_confined: false, network_confined: false };
const base = { setting: true, mode: "confined-files", capabilities: caps, status: "ready" };
const full = {
  ...base,
  selected_mode: "confined-files",
  backends: [
    { backend: "host", status: "ready" },
    { backend: "confined-files", status: "ready" },
    {
      backend: "container",
      status: "not-available",
      reason: "Container image is missing",
      fix: "docker build -t counterweight:local .",
    },
  ],
};

test("an older backend has no mode info and falls back to the setting", () => {
  assert.equal(hasModeInfo(base), false);
  assert.equal(selectedMode(base), "confined-files");
  assert.equal(selectedMode({ ...base, setting: false }), "off");
  assert.equal(hasModeInfo(full), true);
});

test("the selected mode comes from the server when present", () => {
  assert.equal(selectedMode({ ...full, selected_mode: "container" }), "container");
  assert.equal(selectedMode({ ...full, selected_mode: "off", setting: false }), "off");
});

test("only a ready backend that is not already selected can be chosen", () => {
  assert.equal(modeOption(full, "off").selectable, true);
  assert.equal(modeOption(full, "confined-files").selectable, false);
  const container = modeOption(full, "container");
  assert.equal(container.selectable, false);
  assert.equal(container.state, "not-available");
  assert.equal(container.reason, "Container image is missing");
  assert.equal(container.fix, "docker build -t counterweight:local .");
});

test("a failed or unlisted backend is not selectable", () => {
  const failed = {
    ...full,
    backends: full.backends.map((b) => (b.backend === "container" ? { ...b, status: "failed" } : b)),
  };
  assert.equal(modeOption(failed, "container").state, "failed");
  const unlisted = { ...full, backends: [{ backend: "host", status: "ready" }] };
  assert.equal(modeOption(unlisted, "container").selectable, false);
});

test("a docker command is shown as a command, a sentence is not", () => {
  assert.equal(isCommand("docker build -t counterweight:local ."), true);
  assert.equal(isCommand("Start Docker Engine or Docker Desktop, then recheck execution isolation"), false);
  assert.equal(isCommand("Restart the server"), false);
});

test("digests are shortened to 12 hex characters", () => {
  const full64 = `sha256:${"ab12".repeat(16)}`;
  assert.equal(shortDigest(full64), "sha256:ab12ab12ab12");
  assert.equal(shortDigest("not a digest"), "not a digest");
});

test("known families get a label, unknown ones are shown verbatim", () => {
  const t = (k) => en[k.replace("execution.", "")];
  assert.equal(familyLabel("background-tasks", t), "background tasks");
  assert.equal(familyLabel("future-family", t), "future-family");
});

test("the Spanish module has the same placeholders and no em dash", () => {
  const ph = (s) => (s.match(/\{p\d+\}/g) ?? []).sort().join(",");
  for (const key of Object.keys(en)) {
    assert.equal(ph(es[key]), ph(en[key]), key);
    assert.equal(es[key].includes("—"), false, key);
  }
});
