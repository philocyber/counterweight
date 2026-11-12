import assert from "node:assert/strict";
import test from "node:test";
import { activeDurationMs } from "./active-duration.ts";
const at = (n) => new Date(n).toISOString();
test("active duration freezes throughout a permission wait and resumes afterwards", () => {
  const open = [{ started_at: at(10000), ended_at: null }];
  assert.equal(activeDurationMs(1000, 12000, open), 9000);
  assert.equal(activeDurationMs(1000, 900000, open), 9000);
  assert.equal(activeDurationMs(1000, 23000, [{ started_at: at(10000), ended_at: at(20000) }]), 12000);
});
test("concurrent requests share wait time and holds clip to each worker session", () => {
  const holds = [{ started_at: at(1000), ended_at: at(8000) }, { started_at: at(5000), ended_at: at(10000) }];
  assert.equal(activeDurationMs(0, 12000, holds), 3000);
  assert.equal(activeDurationMs(6000, 12000, holds), 2000);
  assert.equal(activeDurationMs(11000, 12000, holds), 1000);
  assert.equal(activeDurationMs(12000, 11000, holds), 0);
});
