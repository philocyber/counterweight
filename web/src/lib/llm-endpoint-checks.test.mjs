import { llmEndpointKind, llmEndpointWarnings } from "./llm-endpoint-checks.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("endpoint kind recognises only the real gateway hosts", () => {
  assert.equal(llmEndpointKind("https://ai-gateway.vercel.sh/v1"), "vercel");
  assert.equal(llmEndpointKind("https://bedrock-runtime.us-east-1.amazonaws.com/anthropic"), "bedrock-runtime");
  assert.equal(llmEndpointKind("https://bedrock-mantle.us-east-1.api.aws/v1"), "bedrock-mantle");
  assert.equal(llmEndpointKind("https://api.openai.com/v1"), "");
  assert.equal(llmEndpointKind("https://bedrock-runtime.us-east-1.amazonaws.com.evil.example/v1"), "");
  assert.equal(llmEndpointKind(""), "");
  assert.equal(llmEndpointKind("not a url"), "");
});

test("correct gateway configurations produce no warnings", () => {
  const ok = [
    ["openai", "https://ai-gateway.vercel.sh/v1", "anthropic/claude-sonnet-5"],
    ["openai-responses", "https://ai-gateway.vercel.sh/v1", "openai/gpt-6-astra"],
    ["anthropic", "https://ai-gateway.vercel.sh", "anthropic/claude-sonnet-5"],
    ["anthropic", "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic/", "us.anthropic.claude-sonnet-5"],
    ["openai", "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", "openai.gpt-oss-120b-1:0"],
    ["openai", "https://bedrock-mantle.us-east-1.api.aws/v1", "openai.gpt-oss-120b"],
    ["openai", "https://bedrock-mantle.us-east-1.api.aws/openai/v1", ""],
    ["anthropic", "https://bedrock-mantle.us-east-1.api.aws/anthropic", "anthropic.claude-sonnet-5"],
    ["openai", "https://api.z.ai/api/paas/v4", "glm-5.3"],
    ["anthropic", "", "claude-opus-5-5"],
  ];
  for (const [format, url, model] of ok) assert.deepEqual(llmEndpointWarnings(format, url, model), [], `${format} ${url}`);
});

test("common mistakes name the specific problem", () => {
  assert.deepEqual(llmEndpointWarnings("anthropic", "https://ai-gateway.vercel.sh/v1", "a/b"), ["warnVercelAnthropicPath"]);
  assert.deepEqual(llmEndpointWarnings("openai", "https://ai-gateway.vercel.sh", "a/b"), ["warnVercelOpenAIPath"]);
  assert.deepEqual(llmEndpointWarnings("openai", "https://ai-gateway.vercel.sh/v1", "claude-sonnet-5"), ["warnVercelModel"]);
  assert.deepEqual(llmEndpointWarnings("anthropic", "https://bedrock-runtime.us-east-1.amazonaws.com", "x"), [
    "warnBedrockAnthropicPath",
  ]);
  assert.deepEqual(llmEndpointWarnings("openai", "https://bedrock-runtime.us-east-1.amazonaws.com/v1", "x"), [
    "warnBedrockOpenAIPath",
  ]);
  assert.deepEqual(llmEndpointWarnings("openai", "https://bedrock-mantle.us-east-1.api.aws/anthropic", "x"), [
    "warnMantleOpenAIPath",
  ]);
  assert.deepEqual(llmEndpointWarnings("openai", "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", "anthropic/x"), [
    "warnBedrockModel",
  ]);
  assert.deepEqual(llmEndpointWarnings("openai", "https://bedrock-runtime.{region}.amazonaws.com/openai/v1", "x"), [
    "warnBedrockRegion",
  ]);
});
