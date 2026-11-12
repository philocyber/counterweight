import { llmEndpointWarnings } from "./llm-endpoint-checks.ts";
import {
  applyLLMProfilePreset,
  BEDROCK_REGIONS,
  bedrockBaseUrlForRegion,
  getLLMProfilePreset,
  LLM_PROFILE_PRESET_IDS,
  LLM_VENDOR_IDS,
  LLM_VENDORS,
  vendorOfPreset,
} from "./llm-profile-presets.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("GLM templates distinguish billing endpoints and use documented model settings", () => {
  const general = getLLMProfilePreset("zai-general");
  const coding = getLLMProfilePreset("zai-coding-reference");
  assert.equal(general.base_url, "https://api.z.ai/api/paas/v4");
  assert.equal(coding.base_url, "https://api.z.ai/api/coding/paas/v4");
  for (const preset of [general, coding]) {
    assert.equal(preset.format, "openai");
    assert.equal(preset.model, "glm-5.3");
    assert.equal(preset.thinking_type, "enabled");
    assert.equal(preset.reasoning_effort, "max");
    assert.equal(preset.context_window_k, 1000);
    assert.equal(preset.max_tokens, 0);
    assert.equal(preset.max_tokens_field, "");
  }
});

test("Claude presets use the documented model IDs, 1M context and an unset thinking field", () => {
  const expected = {
    "claude-fable-5-1": "high",
    "claude-opus-5-5": "medium",
    "claude-sonnet-5-5": "high",
    "claude-haiku-5-5": "medium",
  };
  for (const [id, effort] of Object.entries(expected)) {
    const preset = getLLMProfilePreset(id);
    assert.equal(preset.format, "anthropic");
    assert.equal(preset.model, id, "the preset ID is the API model ID");
    assert.equal(preset.base_url, "", "empty base URL means the official endpoint");
    assert.equal(preset.context_window_k, 1000);
    assert.equal(preset.reasoning_effort, effort, "effort is the documented default");
    assert.equal(preset.thinking_type, "", "adaptive models must not get thinking enabled or disabled");
    assert.equal(preset.max_tokens, 0);
    assert.equal(preset.max_tokens_field, "");
  }
});

test("OpenAI presets use the Responses format, the documented model IDs and the input share of the window", () => {
  for (const [id, model] of [
    ["openai-gpt-6-1-sol", "gpt-6.1-sol"],
    ["openai-gpt-6-luna", "gpt-6-luna"],
  ]) {
    const preset = getLLMProfilePreset(id);
    assert.equal(preset.format, "openai-responses", "Chat Completions has no function calling on GPT-6.1 Sol");
    assert.equal(preset.model, model);
    assert.equal(preset.base_url, "");
    assert.equal(preset.context_window_k, 922, "1,050,000 total minus 128,000 output");
    assert.equal(preset.reasoning_effort, "medium");
    assert.equal(preset.thinking_type, "");
    assert.equal(preset.max_tokens_field, "");
  }
});

test("gateway presets use the documented base URLs, formats and unset reasoning fields", () => {
  const expected = {
    "vercel-gateway-chat": ["openai", "https://ai-gateway.vercel.sh/v1"],
    "vercel-gateway-claude": ["anthropic", "https://ai-gateway.vercel.sh"],
    "bedrock-claude": ["anthropic", "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic"],
    "bedrock-open-models": ["openai", "https://bedrock-mantle.us-east-1.api.aws/v1"],
  };
  for (const [id, [format, baseUrl]] of Object.entries(expected)) {
    const preset = getLLMProfilePreset(id);
    assert.equal(preset.format, format);
    assert.equal(preset.base_url, baseUrl);
    assert.equal(preset.thinking_type, "", "support depends on the model the user picks");
    assert.equal(preset.reasoning_effort, "");
    assert.equal(preset.context_window_k, 200, "conservative until the user declares the real window");
    assert.equal(preset.max_tokens_field, "");
    assert.deepEqual(llmEndpointWarnings(preset.format, preset.base_url, preset.model), [], `${id} must pass its own checks`);
  }
  assert.match(getLLMProfilePreset("vercel-gateway-chat").model, /\//, "Vercel model IDs are provider/model");
  assert.doesNotMatch(getLLMProfilePreset("bedrock-claude").model, /\//, "Bedrock model IDs have no slash");
});

test("every preset belongs to exactly one vendor and vendor lists contain only known presets", () => {
  const seen = new Set();
  for (const v of LLM_VENDOR_IDS) {
    assert.ok(LLM_VENDORS[v].presets.length > 0);
    assert.match(LLM_VENDORS[v].keyUrl, /^https:\/\//);
    for (const id of LLM_VENDORS[v].presets) {
      assert.ok(!seen.has(id), `${id} listed twice`);
      seen.add(id);
      assert.equal(vendorOfPreset(id), v);
      getLLMProfilePreset(id);
    }
  }
  assert.deepEqual([...seen].sort(), [...LLM_PROFILE_PRESET_IDS].sort(), "no preset left without a vendor");
  assert.equal(vendorOfPreset("nope"), undefined);
});

test("Bedrock Region swap rewrites only Bedrock hosts and rejects unsafe values", () => {
  const rt = "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic";
  assert.equal(bedrockBaseUrlForRegion(rt, "eu-west-2"), "https://bedrock-runtime.eu-west-2.amazonaws.com/anthropic");
  assert.equal(
    bedrockBaseUrlForRegion("https://bedrock-mantle.us-east-1.api.aws/v1", "us-west-2"),
    "https://bedrock-mantle.us-west-2.api.aws/v1",
  );
  assert.equal(bedrockBaseUrlForRegion("https://ai-gateway.vercel.sh/v1", "eu-west-2"), "https://ai-gateway.vercel.sh/v1");
  assert.equal(bedrockBaseUrlForRegion(rt, "evil.com/x"), rt);
  for (const r of BEDROCK_REGIONS) assert.deepEqual(llmEndpointWarnings("anthropic", bedrockBaseUrlForRegion(rt, r), "x"), []);
});

test("presets are independent copies", () => {
  const first = getLLMProfilePreset("claude-opus-5-5");
  const mutable = { ...first, model: "changed" };
  assert.equal(getLLMProfilePreset("claude-opus-5-5").model, "claude-opus-5-5");
  assert.equal(mutable.model, "changed");
});

test("applying a template preserves entered secrets and unrelated draft preferences", () => {
  const draft = Object.freeze({
    name: "My profile",
    api_key: "user-entered-key",
    proxy: "http://user:pass@proxy:8080",
    username: "operator",
    session_header_key: "x-session",
    streaming: false,
    rate_per_minute: 7,
    model: "custom-model",
    base_url: "https://previous.example",
  });
  const next = applyLLMProfilePreset(draft, "zai-general");
  for (const key of ["name", "api_key", "proxy", "username", "session_header_key", "streaming", "rate_per_minute"])
    assert.equal(next[key], draft[key]);
  assert.equal(draft.model, "custom-model");
  assert.notEqual(next, draft);
  next.model = "user-selected-model";
  assert.equal(next.model, "user-selected-model", "the preset does not lock model editing");
});

test("unsupported template IDs fail explicitly without changing the draft", () => {
  const draft = { api_key: "keep", model: "keep-model" };
  for (const id of ["", "glm-unknown", "__proto__", "zai-general-extra"]) {
    assert.throws(() => applyLLMProfilePreset(draft, id), RangeError);
    assert.deepEqual(draft, { api_key: "keep", model: "keep-model" });
  }
});
