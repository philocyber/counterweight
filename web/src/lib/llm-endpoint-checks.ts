// Inline pre-flight checks for hosted gateways (Vercel AI Gateway, Amazon Bedrock).
// Mirrors agent/provider_endpoints.go so the form warns before the user clicks Test;
// the server repeats the check, so these are advisory and never block saving.
// Returns warning keys from the "providers" i18n namespace, empty when nothing is wrong.

export type LLMFormat = "openai" | "anthropic" | "openai-responses";

export type LLMEndpointWarning =
  | "warnBedrockRegion"
  | "warnVercelAnthropicPath"
  | "warnVercelOpenAIPath"
  | "warnVercelModel"
  | "warnBedrockAnthropicPath"
  | "warnBedrockOpenAIPath"
  | "warnMantleOpenAIPath"
  | "warnBedrockModel";

const REGION_PLACEHOLDER = /\{region\}|<region>|<your-region>|\$\{?region\}?|\{\{region\}\}/;

export function llmEndpointKind(baseUrl: string): "vercel" | "bedrock-runtime" | "bedrock-mantle" | "" {
  let host = "";
  try {
    host = new URL(baseUrl.trim()).hostname.toLowerCase();
  } catch {
    return "";
  }
  if (host === "ai-gateway.vercel.sh") return "vercel";
  if (/^bedrock-runtime\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$/.test(host)) return "bedrock-runtime";
  if (/^bedrock-mantle\.[a-z0-9-]+\.api\.aws$/.test(host)) return "bedrock-mantle";
  return "";
}

export function llmEndpointWarnings(format: LLMFormat, baseUrl: string, model: string): LLMEndpointWarning[] {
  const out: LLMEndpointWarning[] = [];
  const base = baseUrl.trim();
  const m = model.trim();
  if (REGION_PLACEHOLDER.test(base)) return ["warnBedrockRegion"];
  const kind = llmEndpointKind(base);
  if (!kind) return out;
  let path = "";
  try {
    path = new URL(base).pathname.replace(/\/+$/, "");
  } catch {
    return out;
  }
  const anthropic = format === "anthropic";
  if (kind === "vercel") {
    if (anthropic && path !== "") out.push("warnVercelAnthropicPath");
    if (!anthropic && path !== "/v1") out.push("warnVercelOpenAIPath");
    if (m && !m.includes("/")) out.push("warnVercelModel");
    return out;
  }
  if (kind === "bedrock-runtime") {
    if (anthropic && path !== "/anthropic") out.push("warnBedrockAnthropicPath");
    if (!anthropic && path !== "/openai/v1") out.push("warnBedrockOpenAIPath");
  } else {
    if (anthropic && path !== "/anthropic") out.push("warnBedrockAnthropicPath");
    if (!anthropic && path !== "/v1" && path !== "/openai/v1") out.push("warnMantleOpenAIPath");
  }
  if (m.includes("/")) out.push("warnBedrockModel");
  return out;
}
