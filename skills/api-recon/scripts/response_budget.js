'use strict';

function createResponseBudget(options = {}) {
  const positive = (value, fallback) => {
    const n = value === undefined ? fallback : value;
    if (!Number.isSafeInteger(n) || n <= 0) throw new Error('Response budgets must be positive safe integers');
    return n;
  };
  const budget = {
    maxResponseBytes: positive(options.maxResponseBytes, 8 * 1024 * 1024),
    maxTotalBytes: positive(options.maxTotalBytes, 64 * 1024 * 1024),
    maxResponses: positive(options.maxResponses, 2048),
    maxConcurrent: positive(options.maxConcurrent, 8),
    responseTimeoutMs: positive(options.responseTimeoutMs, 20000),
    bytes: 0, responses: 0, active: 0, omitted: 0,
  };
  budget.acquire = () => {
    if (budget.responses >= budget.maxResponses || budget.bytes >= budget.maxTotalBytes || budget.active >= budget.maxConcurrent) {
      budget.omitted++;
      return null;
    }
    budget.responses++;
    budget.active++;
    let released = false;
    return () => { if (!released) { released = true; budget.active--; } };
  };
  return budget;
}

async function readResponseText(response, budget) {
  if (!response.body) return { text: '', truncated: false };
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0, timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('Response read deadline exceeded')), budget.responseTimeoutMs);
  });
  try {
    for (;;) {
      const { done, value } = await Promise.race([reader.read(), timeout]);
      if (done) return { text: Buffer.concat(chunks, size).toString('utf8'), truncated: false };
      // Other readers can consume the allowance while this read is pending.
      const remaining = Math.min(budget.maxResponseBytes - size, budget.maxTotalBytes - budget.bytes);
      const keep = Math.min(value.byteLength, Math.max(0, remaining));
      if (keep) { chunks.push(Buffer.from(value.subarray(0, keep))); size += keep; budget.bytes += keep; }
      if (value.byteLength > remaining) {
        void reader.cancel().catch(() => {});
        return { text: Buffer.concat(chunks, size).toString('utf8'), truncated: true };
      }
    }
  } catch (error) {
    void reader.cancel().catch(() => {});
    throw error;
  } finally { clearTimeout(timer); }
}

module.exports = { createResponseBudget, readResponseText };
