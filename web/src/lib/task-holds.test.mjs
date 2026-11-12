import { approvalHold, pauseKind, platformHold, platformHoldKind } from "./task-holds.ts";
import assert from "node:assert/strict";
import test from "node:test";

const hold = (outcome, ended_at = null, reason = "") => ({ id: 1, started_at: "t", ended_at, outcome, reason });
const task = (...holds) => ({ approval_state: { holds, pending: [] } });

test("holds are classified by outcome", () => {
  assert.equal(platformHoldKind({ outcome: "budget" }), "budget");
  assert.equal(platformHoldKind({ outcome: "execution" }), "execution");
  assert.equal(platformHoldKind({ outcome: "" }), null);
  assert.equal(platformHoldKind({ outcome: "approved" }), null);
});

test("a permission hold is an approval wait; budget and isolation holds are not", () => {
  assert.ok(approvalHold(task(hold(""))));
  assert.equal(approvalHold(task(hold("budget"))), undefined);
  assert.equal(approvalHold(task(hold("execution"))), undefined);
  assert.equal(approvalHold(task(hold("", "2026-01-01"))), undefined);
  assert.equal(approvalHold(undefined), undefined);
});

test("pauseKind reports the platform pause of a task", () => {
  assert.equal(pauseKind(task(hold("budget"))), "budget");
  assert.equal(pauseKind(task(hold("execution"))), "execution");
  assert.equal(platformHold(task(hold("budget")))?.kind, "budget");
  assert.equal(pauseKind(task(hold("budget", "2026-01-01"))), null);
  assert.equal(pauseKind(task(hold(""))), null);
  assert.equal(pauseKind(task()), null);
  assert.equal(pauseKind(null), null);
  // Outcome decides, never reason text: an approval hold that mentions isolation stays an approval hold.
  assert.equal(pauseKind(task(hold("", null, "Isolation could not be established"))), null);
});
