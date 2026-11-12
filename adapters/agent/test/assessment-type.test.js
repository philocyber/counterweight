import test from "node:test";
import assert from "node:assert/strict";
import { createTools, executeTool } from "../src/tools.js";

test("review creation forwards the type and repository with a clone timeout", async () => {
  let sent;
  const client = { config: { allowWrites: true, timeoutMs: 30000 }, request: async (path, options) => { sent = { path, ...options }; return { id: "1" }; } };
  const create = createTools(client).find((tool) => tool.name === "counterweight_create_task");
  await executeTool(create, { description: "Fixture", goal: "Review source", assessment_type: "code_review", repository: "example/fixture", mode: "light", instructions: "Focus on authorization" });
  assert.equal(sent.body.assessment_type, "code_review");
  assert.equal(sent.body.repository, "example/fixture");
  assert.equal(sent.body.mode, "light");
  assert.equal(sent.timeoutMs, 660000);
  assert.equal(create.schema.parse({ description: "Fixture", goal: "Review" }).assessment_type, "general");
  assert.throws(() => create.schema.parse({ description: "Fixture", goal: "Review", assessment_type: "guessed" }));
});
