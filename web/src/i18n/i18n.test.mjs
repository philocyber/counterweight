import { mentionKinds, mentionSearch, mentionToken, selectedMentions } from "../lib/chat-mentions.ts";
import { catalogs } from "./catalog.ts";
import { normalizeLocale, readCookieLocale, withLocaleHeaders, withLocaleQuery } from "./config.ts";
import { cloneDemoFixture } from "./demo-fixture.ts";
import { createFormatters, interpolate, placeholders, tags } from "./format.ts";
import { SRC_ROOT, scanFile, sourceFiles } from "./han-scan.mjs";
import { localizedFetch } from "./request.ts";
import {
  __resetLocaleForTests,
  getLocale,
  initializeLocale,
  localeHeaders,
  translate,
  translateIn,
} from "./runtime.ts";
import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";

function browser(stored = null, cookie = "") {
  const values = new Map(stored ? [["counterweight.locale", stored]] : []);
  globalThis.window = {
    localStorage: { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) },
  };
  globalThis.document = { cookie, documentElement: { lang: "en" } };
  return values;
}
function reset() {
  delete globalThis.window;
  delete globalThis.document;
  __resetLocaleForTests();
}

test("English is the default; Spanish is selectable; other preferences fall back", () => {
  reset();
  assert.equal(getLocale(), "en");
  assert.equal(normalizeLocale("en-US"), "en");
  assert.equal(normalizeLocale("es-ES"), "es");
  assert.equal(normalizeLocale("es"), "es");
  assert.equal(normalizeLocale("fr-FR"), null);
  assert.equal(normalizeLocale("zh-CN"), null);
  assert.equal(readCookieLocale("counterweight_locale=en"), "en");
  assert.equal(readCookieLocale("counterweight_locale=es"), "es");
  assert.equal(readCookieLocale("counterweight_locale=%E0%A4%A"), null);
  browser("unsupported", "counterweight_locale=fr");
  initializeLocale();
  assert.equal(getLocale(), "en");
  reset();
  browser("es", "");
  initializeLocale();
  assert.equal(getLocale(), "es");
  reset();
  browser("unsupported", "counterweight_locale=%bad");
  initializeLocale();
  assert.equal(getLocale(), "en");
  reset();
});

test("the Spanish catalog mirrors every English key and is free of Han literals", () => {
  assert.deepEqual(Object.keys(catalogs.es).sort(), Object.keys(catalogs.en).sort());
  for (const key of Object.keys(catalogs.es)) {
    assert.ok(catalogs.es[key].length, key);
    assert.doesNotMatch(catalogs.es[key], /\p{Script=Han}/u, key);
  }
});

test("the English catalog is complete and free of untranslated or Han literals", () => {
  assert.ok(Object.keys(catalogs.en).length >= 3000);
  for (const key of Object.keys(catalogs.en)) {
    assert.ok(catalogs.en[key].length, key);
    assert.doesNotMatch(catalogs.en[key], /Untranslated|\p{Script=Han}/u, key);
  }
  assert.equal(
    interpolate("Task {id}: {name}", { id: 7, name: "original {unchanged}" }),
    "Task 7: original {unchanged}",
  );
  assert.equal(interpolate("{missing}", {}), "{missing}");
  assert.deepEqual(placeholders("Hi {b} {a}"), ["a", "b"]);
  assert.deepEqual(tags("<b>x</b> {y}"), ["/b", "b"]);
});

test("locale transport preserves caller headers, queries, and fragments", async () => {
  assert.equal(withLocaleQuery("/api/report?task=3#section", "en"), "/api/report?task=3&lang=en#section");
  assert.equal(withLocaleQuery("/api/report?lang=en", "en"), "/api/report?lang=en");
  assert.deepEqual(withLocaleHeaders("en", { Authorization: "Bearer demo", "Content-Type": "application/json" }), {
    "Accept-Language": "en",
    Authorization: "Bearer demo",
    "Content-Type": "application/json",
  });
  reset();
  browser();
  const original = globalThis.fetch;
  let observed;
  globalThis.fetch = async (input, init) => {
    observed = { input, init };
    return new Response("ok");
  };
  try {
    await localizedFetch("/api/report?task=1", { headers: { Authorization: "Bearer example" } });
    assert.equal(observed.input, "/api/report?task=1&lang=en");
    assert.equal(new Headers(observed.init.headers).get("accept-language"), "en");
    assert.equal(new Headers(observed.init.headers).get("authorization"), "Bearer example");
  } finally {
    globalThis.fetch = original;
    reset();
  }
});

test("authored demo data resolves through the catalog; edited user values stay literal", () => {
  reset();
  browser();
  const authored = translate("english.e004");
  const fixture = cloneDemoFixture({ title: authored, nested: [{ name: authored }] }, true);
  assert.equal(fixture.title, authored);
  assert.equal(fixture.nested[0].name, authored);
  fixture.title = "My dashboard original";
  const runtimeCopy = cloneDemoFixture(fixture);
  assert.equal(fixture.title, "My dashboard original");
  assert.equal(runtimeCopy.title, "My dashboard original");
  reset();
});

test("mention wire tokens remain stable for backend compatibility", () => {
  reset();
  browser();
  const finding = mentionKinds.find((item) => item.kind === "finding");
  assert.equal(finding.wire, "finding");
  assert.ok(finding.label.length);
  assert.equal(mentionSearch("finding login").kind, "finding");
  const token = mentionToken({ kind: "finding", id: 12, label: "user label", description: "" });
  assert.equal(token, "@[finding#12 user label]");
  const selected = selectedMentions(token)[0];
  assert.ok(selected.label.startsWith(finding.label));
  assert.match(selected.label, /#12 · user label/);
  reset();
});

test("date and number formatters use the English locale", () => {
  const formatters = createFormatters("en");
  assert.equal(formatters.number(12345), new Intl.NumberFormat("en-US").format(12345));
  assert.equal(typeof formatters.date(new Date("2026-10-02T00:00:00Z"), { timeZone: "UTC" }), "string");
  assert.equal(translateIn("en", "english.e004"), translate("english.e004"));
});

test("authored UI contains no untranslated Han literals; legacy wire parsers are explicit exceptions", () => {
  const failures = [];
  for (const file of sourceFiles()) {
    const rel = path.relative(SRC_ROOT, file).split(path.sep).join("/");
    if (rel.endsWith(".test.mjs")) continue; // Tests intentionally preserve multilingual user input.
    for (const hit of scanFile(file)) {
      if (rel === "lib/company-scope.ts" && hit.text === "/icp|备案/i") continue;
      failures.push(`${rel}:${hit.line} ${hit.text}`);
    }
  }
  assert.deepEqual(failures, []);
});

test("authored source and build configuration comments are English", async () => {
  const { hasHanComment } = await import("./han-scan.mjs");
  const files = [...sourceFiles(), path.resolve("next.config.mjs")];
  assert.deepEqual(
    files.filter((file) => hasHanComment(file)).map((file) => path.relative(SRC_ROOT, file)),
    [],
  );
});
