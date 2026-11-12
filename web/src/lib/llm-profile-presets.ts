// Presets change form fields only; they never contact a provider or save anything.
//
// Z.ai documentation verified 2026-10-03:
//   https://docs.z.ai/guides/llm/glm-5.3
//   https://zcode.z.ai/en/docs/configuration
// Anthropic model pages verified 2026-10-21 (model ID, 1M context, 128K max output, default effort):
//   https://platform.claude.com/docs/en/models/fable-5-1/overview
//   https://platform.claude.com/docs/en/models/opus-5-5/overview
//   https://platform.claude.com/docs/en/models/sonnet-5-5/overview
//   https://platform.claude.com/docs/en/models/haiku-5-5/overview
//
// OpenAI model pages (gpt-6.1-sol, gpt-6-luna) as indexed on 2026-10-21: 1,050,000 context, 128,000 max
// output, so max input is 922,000 and that is the window declared here. GPT-6.1 Sol does not support function
// calling on Chat Completions, so both OpenAI presets use the Responses format.
//   https://developers.openai.com/api/docs/models/gpt-6.1-sol
//   https://developers.openai.com/api/docs/models/gpt-6-luna
//
// Gateway presets verified 2026-10-21 (base URLs, auth, model ID styles):
//   https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions
//   https://vercel.com/docs/ai-gateway/sdks-and-apis/anthropic-messages-api
//   https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html
//   https://docs.aws.amazon.com/bedrock/latest/userguide/inference-messages-api.html
//   https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html
//
// Claude presets leave thinking.type empty on purpose: these models use adaptive thinking, Opus 5.5 and
// Fable 5.1 cannot turn it off, and effort is the control. Effort is each model's documented default.
export const LLM_PROFILE_PRESET_IDS = [
  "zai-general",
  "zai-coding-reference",
  "claude-fable-5-1",
  "claude-opus-5-5",
  "claude-sonnet-5-5",
  "claude-haiku-5-5",
  "openai-gpt-6-1-sol",
  "openai-gpt-6-luna",
  "vercel-gateway-chat",
  "vercel-gateway-claude",
  "bedrock-claude",
  "bedrock-open-models",
] as const;
export type LLMProfilePresetId = (typeof LLM_PROFILE_PRESET_IDS)[number];

// Suggested profile name, used only when the name field is still empty.
export const LLM_PROFILE_PRESET_NAMES: Record<LLMProfilePresetId, string> = {
  "zai-general": "GLM-5.3",
  "zai-coding-reference": "GLM-5.3 Coding Plan",
  "claude-fable-5-1": "Claude Fable 5.1",
  "claude-opus-5-5": "Claude Opus 5.5",
  "claude-sonnet-5-5": "Claude Sonnet 5.5",
  "claude-haiku-5-5": "Claude Haiku 5.5",
  "openai-gpt-6-1-sol": "GPT-6.1 Sol",
  "openai-gpt-6-luna": "GPT-6 Luna",
  "vercel-gateway-chat": "Vercel AI Gateway",
  "vercel-gateway-claude": "Vercel AI Gateway (Claude)",
  "bedrock-claude": "Bedrock Claude",
  "bedrock-open-models": "Bedrock open models",
};

export interface LLMProfilePresetFields {
  format: "openai" | "anthropic" | "openai-responses";
  model: string;
  base_url: string;
  thinking_type: "enabled" | "";
  reasoning_effort: string;
  context_window_k: number;
  max_tokens: number;
  max_tokens_field: "";
}

const ZAI_GENERAL = "https://api.z.ai/api/paas/v4";
const ZAI_CODING = "https://api.z.ai/api/coding/paas/v4";
const VERCEL_GATEWAY = "https://ai-gateway.vercel.sh";
const VERCEL_GATEWAY_V1 = `${VERCEL_GATEWAY}/v1`;
// Bedrock endpoints are per Region; us-east-1 is only a starting point and must match the Region the
// Bedrock API key was created in.
const BEDROCK_RUNTIME_ANTHROPIC = "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic";
const BEDROCK_MANTLE_OPENAI = "https://bedrock-mantle.us-east-1.api.aws/v1";

const PRESETS: Record<LLMProfilePresetId, LLMProfilePresetFields> = {
  "zai-general": zai(ZAI_GENERAL),
  "zai-coding-reference": zai(ZAI_CODING),
  "claude-fable-5-1": claude("claude-fable-5-1", "high"),
  "claude-opus-5-5": claude("claude-opus-5-5", "medium"),
  "claude-sonnet-5-5": claude("claude-sonnet-5-5", "high"),
  "claude-haiku-5-5": claude("claude-haiku-5-5", "medium"),
  "openai-gpt-6-1-sol": openaiResponses("gpt-6.1-sol", "medium"),
  "openai-gpt-6-luna": openaiResponses("gpt-6-luna", "medium"),
  "vercel-gateway-chat": gateway("openai", VERCEL_GATEWAY_V1, "anthropic/claude-sonnet-5"),
  "vercel-gateway-claude": gateway("anthropic", VERCEL_GATEWAY, "anthropic/claude-sonnet-5"),
  "bedrock-claude": gateway("anthropic", BEDROCK_RUNTIME_ANTHROPIC, "us.anthropic.claude-sonnet-5"),
  "bedrock-open-models": gateway("openai", BEDROCK_MANTLE_OPENAI, "openai.gpt-oss-120b"),
};

function zai(baseUrl: string): LLMProfilePresetFields {
  return {
    format: "openai",
    model: "glm-5.3",
    base_url: baseUrl,
    thinking_type: "enabled",
    reasoning_effort: "max",
    context_window_k: 1000,
    // Preserve the existing default: omit the output limit and use provider defaults.
    max_tokens: 0,
    max_tokens_field: "",
  };
}

function claude(model: string, effort: string): LLMProfilePresetFields {
  return {
    format: "anthropic",
    model,
    // Empty uses the official Anthropic endpoint.
    base_url: "",
    thinking_type: "",
    reasoning_effort: effort,
    context_window_k: 1000,
    max_tokens: 0,
    max_tokens_field: "",
  };
}

// Multi-model gateways (Vercel AI Gateway, Amazon Bedrock). The model is a starting point taken from the
// providers' documentation; Load models lists what the key can actually use. Thinking and effort stay unset
// because support depends on whichever model the user picks, and gateways reject fields a model lacks.
// The window is a conservative 200K for the same reason; raise it to the chosen model's real window.
function gateway(format: "openai" | "anthropic", baseUrl: string, model: string): LLMProfilePresetFields {
  return {
    format,
    model,
    base_url: baseUrl,
    thinking_type: "",
    reasoning_effort: "",
    context_window_k: 200,
    max_tokens: 0,
    max_tokens_field: "",
  };
}

function openaiResponses(model: string, effort: string): LLMProfilePresetFields {
  return {
    format: "openai-responses",
    model,
    // Empty uses the official OpenAI endpoint.
    base_url: "",
    thinking_type: "",
    reasoning_effort: effort,
    // 1,050,000 total minus 128,000 max output.
    context_window_k: 922,
    max_tokens: 0,
    max_tokens_field: "",
  };
}

// Vendor-first selection: picking a vendor applies its first preset and offers the rest.
// keyUrl is where the user creates the API key; it is only ever shown as a link.
export const LLM_VENDOR_IDS = ["anthropic", "openai", "zai", "vercel", "bedrock"] as const;
export type LLMVendorId = (typeof LLM_VENDOR_IDS)[number];

export const LLM_VENDORS: Record<LLMVendorId, { presets: LLMProfilePresetId[]; keyUrl: string }> = {
  anthropic: {
    presets: ["claude-opus-5-5", "claude-fable-5-1", "claude-sonnet-5-5", "claude-haiku-5-5"],
    keyUrl: "https://platform.claude.com/settings/keys",
  },
  openai: {
    presets: ["openai-gpt-6-1-sol", "openai-gpt-6-luna"],
    keyUrl: "https://platform.openai.com/api-keys",
  },
  zai: { presets: ["zai-general", "zai-coding-reference"], keyUrl: "https://z.ai/manage-apikey/apikey-list" },
  vercel: {
    presets: ["vercel-gateway-chat", "vercel-gateway-claude"],
    keyUrl: "https://vercel.com/d?to=%2F%5Bteam%5D%2F%7E%2Fai-gateway%2Fapi-keys&title=AI+Gateway+API+Keys",
  },
  bedrock: {
    presets: ["bedrock-claude", "bedrock-open-models"],
    keyUrl: "https://console.aws.amazon.com/bedrock/home#/api-keys",
  },
};

export function vendorOfPreset(id: string): LLMVendorId | undefined {
  return LLM_VENDOR_IDS.find((v) => (LLM_VENDORS[v].presets as readonly string[]).includes(id));
}

// Common Bedrock Regions; availability differs per model and the Base URL stays editable for any other.
export const BEDROCK_REGIONS = [
  "us-east-1",
  "us-east-2",
  "us-west-2",
  "eu-west-1",
  "eu-west-2",
  "eu-central-1",
  "ap-northeast-1",
  "ap-southeast-1",
  "ap-southeast-2",
  "ap-south-1",
] as const;
export const DEFAULT_BEDROCK_REGION = "us-east-1";

// Swaps the Region in a bedrock-runtime / bedrock-mantle Base URL; any other URL is returned unchanged.
export function bedrockBaseUrlForRegion(baseUrl: string, region: string): string {
  if (!/^[a-z0-9-]+$/.test(region)) return baseUrl;
  return baseUrl.replace(/^(https:\/\/bedrock-(?:runtime|mantle)\.)[a-z0-9-]+(\.)/, `$1${region}$2`);
}

export function getLLMProfilePreset(id: string): Readonly<LLMProfilePresetFields> {
  if (!(LLM_PROFILE_PRESET_IDS as readonly string[]).includes(id)) {
    throw new RangeError(`Unknown LLM profile preset: ${id}`);
  }
  return { ...PRESETS[id as LLMProfilePresetId] };
}

// Unknown fields, including credentials and profile names, survive unchanged.
// The original draft is not mutated, and no request or persistence occurs here.
export function applyLLMProfilePreset<T extends object>(draft: T, id: string): T & LLMProfilePresetFields {
  return { ...draft, ...getLLMProfilePreset(id) };
}
