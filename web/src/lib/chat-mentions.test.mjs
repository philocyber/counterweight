import { activeMention, mentionSearch, mentionToken, selectedMentions } from "./chat-mentions.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("mention trigger supports non-ASCII query text and cursor placement without hijacking email", () => {
  assert.equal(activeMention("user@example.com", 16), null);
  assert.equal(activeMention("selected @[finding#1 X]", 18), null);
  assert.deepEqual(activeMention("see @café after the text", 9), { start: 4, end: 9, query: "café" });
  assert.equal(activeMention("@café\nnext line", 9), null);
});

test("categories, partial-word aliases, IP and keyword search", () => {
  assert.equal(mentionSearch("").categories.length, 9);
  assert.equal(mentionSearch("f").categories[0].kind, "finding");
  assert.equal(mentionSearch("finding").kind, "finding");
  assert.equal(mentionSearch("finding SQL injection").query, "SQL injection");
  assert.equal(mentionSearch("ip 192.0.2.1").kind, "ip");
  assert.equal(mentionSearch("api GET /api").query, "GET /api");
  assert.equal(mentionSearch("acme.com").kind, "");
});

test("tokens roundtrip labels and removing one reference preserves its neighbors", () => {
  const first = mentionToken({ kind: "finding", id: 12, label: "Title[1]\ndescription" });
  const second = mentionToken({ kind: "ip", id: 13, label: "192.0.2.1" });
  const value = `Check ${first} and ${second}`;
  const selected = selectedMentions(value);
  assert.equal(selected.length, 2);
  assert.equal(selected[0].label, "Vulnerability #12 · Title(1) description");
  assert.equal(first, "@[finding#12 Title(1) description]", "wire token stays a stable backend contract");
  const next = value.slice(0, selected[0].start) + value.slice(selected[0].start + selected[0].token.length);
  assert.equal(selectedMentions(next)[0].token, second);
});
