import { mergeDictation } from "./speech-dictation.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("dictation appends recognized phrases to the text already in the field", () => {
  const results = [
    { isFinal: true, 0: { transcript: "list the tasks" } },
    { isFinal: false, 0: { transcript: " for today" } },
  ];
  assert.equal(mergeDictation("", results), "list the tasks for today");
  assert.equal(mergeDictation("Please", results), "Please list the tasks for today");
  assert.equal(mergeDictation("Please  ", results), "Please list the tasks for today");
  assert.equal(mergeDictation("Keep this", []), "Keep this");
});
