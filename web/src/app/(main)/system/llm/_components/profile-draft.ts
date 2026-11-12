"use client";

import * as React from "react";

import { toast } from "sonner";

import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { llmEndpointWarnings } from "@/lib/llm-endpoint-checks";
import {
  applyLLMProfilePreset,
  bedrockBaseUrlForRegion,
  DEFAULT_BEDROCK_REGION,
  LLM_PROFILE_PRESET_NAMES,
  LLM_VENDORS,
  type LLMProfilePresetId,
  type LLMVendorId,
  vendorOfPreset,
} from "@/lib/llm-profile-presets";
import type { ChatGPTModel, LLMProfile, LLMRetryOverride } from "@/lib/types";

import { ZERO_OVERRIDE } from "./retry";

// thinking.type and reasoning_effort are independent fields and must be set separately;
// some APIs have no thinking field and enable reasoning through effort alone.
// Stored empty strings omit the field. Radix Select rejects empty values, so UI uses none
// as a sentinel converted to/from empty strings by NONE/fromStore/toStore.
export const NONE = "none";
export const fromStore = (v?: string) => (v ? v : NONE);
export const toStore = (v: string) => (v === NONE ? "" : v);

// Placeholder key for local servers that ignore authentication; the profile still needs a non-empty key.
export const LOCAL_PLACEHOLDER_KEY = "local";
// Starting values for a local or self-hosted OpenAI-compatible server (see docs/choose-a-model.md).
const LOCAL_CONTEXT_WINDOW_K = "128";
const LOCAL_OUTPUT_LIMIT = "8192";
const LOCAL_PROFILE_NAME = "Local model";

const priceText = (v?: number | null) => (v === null || v === undefined ? "" : String(v));
const priceValue = (v: string) => (v.trim() === "" ? null : Number(v));

// True when the price fields differ from what the profile has stored (a new profile stores nothing).
function priceChanged(
  profile: LLMProfile | null,
  now: { priceInput: string; priceCachedInput: string; priceOutput: string; priceCurrency: string },
): boolean {
  return (
    now.priceInput.trim() !== priceText(profile?.price_input_per_million) ||
    now.priceCachedInput.trim() !== priceText(profile?.price_cached_input_per_million) ||
    now.priceOutput.trim() !== priceText(profile?.price_output_per_million) ||
    now.priceCurrency.trim().toUpperCase() !== (profile?.price_currency ?? "").toUpperCase()
  );
}

type Format = "anthropic" | "openai" | "openai-responses";

// All state, validation and API calls of the model-profile editor. The guided "Connect a model" flow and the
// full edit form render the same draft, so request payloads and field semantics are identical in both.
export function useProfileDraft({
  profile,
  open,
  onOpenChange,
  onSaved,
  initialSubscription,
}: {
  profile: LLMProfile | null; // Null means creation.
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSaved: (id: string) => void;
  initialSubscription: boolean;
}) {
  "use no memo";
  const { t: swt } = useI18n();

  const isNew = !profile;
  // New API-key profiles go through the guided flow; editing and the ChatGPT sign-in use the full form.
  const [guided, setGuided] = React.useState(false);
  const [useSubscription, setUseSubscription] = React.useState(false);
  const [subscriptionId, setSubscriptionId] = React.useState("");
  const [cyberProgram, setCyberProgram] = React.useState("standard");
  const [preset, setPreset] = React.useState<LLMProfilePresetId | "">("");
  const [vendor, setVendor] = React.useState<LLMVendorId | "">("");
  const [region, setRegion] = React.useState<string>(DEFAULT_BEDROCK_REGION);
  const [name, setName] = React.useState("");
  const [format, setFormat] = React.useState<Format>("anthropic");
  const [model, setModel] = React.useState("");
  const [baseUrl, setBaseUrl] = React.useState("");
  const [proxy, setProxy] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [keyHint, setKeyHint] = React.useState("");
  const [rps, setRps] = React.useState("0");
  const [rpm, setRpm] = React.useState("0");
  const [cw, setCw] = React.useState("0"); // Context window in K tokens; 0 defaults to 200K.
  const [thinkingType, setThinkingType] = React.useState(NONE);
  const [effort, setEffort] = React.useState(NONE);
  const [priority, setPriority] = React.useState("0"); // Rotation priority; higher values first.
  const [poolExclude, setPoolExclude] = React.useState(false);
  const [streaming, setStreaming] = React.useState(true); // True means streaming (default), false means nonstreaming.
  const [maxTokens, setMaxTokens] = React.useState("0"); // Per-response output limit; 0 omits the field.
  const [maxTokensField, setMaxTokensField] = React.useState(NONE); // Output-limit field name; NONE means max_tokens.
  const [sessionHeaderKey, setSessionHeaderKey] = React.useState(""); // Custom session header name; blank omits it.
  const [retry, setRetry] = React.useState<LLMRetryOverride>(ZERO_OVERRIDE); // Profile retry overrides; all zeros follow global settings.
  // Optional prices per million tokens, kept as strings like every number field; empty means unset.
  const [priceInput, setPriceInput] = React.useState("");
  const [priceCachedInput, setPriceCachedInput] = React.useState("");
  const [priceOutput, setPriceOutput] = React.useState("");
  const [priceCurrency, setPriceCurrency] = React.useState("");
  const [testing, setTesting] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [models, setModels] = React.useState<string[]>([]);
  const [subscriptionModels, setSubscriptionModels] = React.useState<ChatGPTModel[]>([]);
  const [subscriptionModelsRefresh, setSubscriptionModelsRefresh] = React.useState(0);
  const [loadingModels, setLoadingModels] = React.useState(false);
  const [modelsOpen, setModelsOpen] = React.useState(false);
  // Latest connection test, tied to the settings it ran with so a later edit invalidates it.
  const [tested, setTested] = React.useState<{ sig: string; ok: boolean; message: string; detail: string } | null>(
    null,
  );

  // Populate from the supplied profile on every open, or reset defaults for creation.
  // Closing and reopening starts fresh without stale values from another profile.
  React.useEffect(() => {
    if (!open) return;
    setGuided(!profile && !initialSubscription);
    setUseSubscription(!!profile?.subscription_id || initialSubscription);
    setSubscriptionId(profile?.subscription_id ?? "");
    setCyberProgram(profile?.cyber_access_program || "standard");
    setPreset("");
    setVendor("");
    setRegion(DEFAULT_BEDROCK_REGION);
    setName(profile?.name ?? "");
    setFormat(
      initialSubscription && !profile
        ? "openai-responses"
        : profile?.format === "openai" || profile?.format === "openai-responses"
          ? profile.format
          : "anthropic",
    );
    setModel(profile?.model ?? "");
    setBaseUrl(profile?.base_url ?? (initialSubscription ? "https://api.openai.com/v1" : ""));
    setProxy(profile?.subscription_id ? "" : (profile?.proxy ?? ""));
    setRps(String(profile?.rate_per_second ?? 0));
    setRpm(String(profile?.rate_per_minute ?? 0));
    setCw(String(profile?.context_window_k ?? 0));
    setThinkingType(profile?.subscription_id ? NONE : fromStore(profile?.thinking_type));
    setEffort(fromStore(profile?.reasoning_effort));
    setPriority(String(profile?.priority ?? 0));
    setPoolExclude(profile?.pool_exclude ?? false);
    setStreaming(profile?.streaming ?? true);
    setMaxTokens(profile?.subscription_id ? "0" : String(profile?.max_tokens ?? 0));
    setMaxTokensField(fromStore(profile?.max_tokens_field));
    setSessionHeaderKey(profile?.subscription_id ? "" : (profile?.session_header_key ?? ""));
    setRetry(profile?.retry ?? ZERO_OVERRIDE);
    setPriceInput(priceText(profile?.price_input_per_million));
    setPriceCachedInput(priceText(profile?.price_cached_input_per_million));
    setPriceOutput(priceText(profile?.price_output_per_million));
    setPriceCurrency(profile?.price_currency ?? "");
    setApiKey("");
    setKeyHint(profile?.api_key_hint ?? "");
    setModels([]);
    setSubscriptionModels([]);
    setModelsOpen(false);
    setTested(null);
  }, [open, profile, initialSubscription]);

  React.useEffect(() => {
    if (!open || !useSubscription || !subscriptionId) return;
    let active = true;
    setLoadingModels(true);
    void api
      .fetchLLMModels("openai-responses", "https://api.openai.com/v1", "", "", undefined, subscriptionId)
      .then((result) => {
        if (!active) return;
        if (!result.ok) throw new Error(result.error || "Could not load ChatGPT models");
        const choices = result.model_choices ?? (result.models ?? []).map((slug) => ({ slug, display_name: slug }));
        setSubscriptionModels(choices);
        setModel((current) => (choices.some((choice) => choice.slug === current) ? current : (choices[0]?.slug ?? "")));
      })
      .catch((error: Error) => {
        if (active) toast.error(error.message);
      })
      .finally(() => {
        if (active) setLoadingModels(false);
      });
    return () => {
      active = false;
    };
  }, [open, useSubscription, subscriptionId, subscriptionModelsRefresh]);

  // Picking a vendor applies its first template at once, so one click fills the whole form.
  function applyVendor(v: string) {
    const id = v as LLMVendorId;
    setVendor(id);
    applyPreset(LLM_VENDORS[id].presets[0], id === "bedrock" ? region : undefined);
  }

  function applyRegion(r: string) {
    setRegion(r);
    setBaseUrl((u) => bedrockBaseUrlForRegion(u, r));
  }

  function applyPreset(id: string, bedrockRegion?: string) {
    if (!isNew) return;
    const next = applyLLMProfilePreset({ name, api_key: apiKey, proxy, session_header_key: sessionHeaderKey }, id);
    setPreset(id as LLMProfilePresetId);
    // Replace a name that a previous template suggested; never one the user typed.
    if (!name.trim() || Object.values(LLM_PROFILE_PRESET_NAMES).includes(name) || name === LOCAL_PROFILE_NAME) {
      setName(LLM_PROFILE_PRESET_NAMES[id as LLMProfilePresetId]);
    }
    // Drop the local placeholder key; a real key is never touched.
    if (apiKey === LOCAL_PLACEHOLDER_KEY) setApiKey("");
    setFormat(next.format);
    setModel(next.model);
    // Bedrock templates keep the Region already chosen instead of resetting to the default.
    setBaseUrl(
      vendorOfPreset(id) === "bedrock"
        ? bedrockBaseUrlForRegion(next.base_url, bedrockRegion ?? region)
        : next.base_url,
    );
    setThinkingType(fromStore(next.thinking_type));
    setEffort(fromStore(next.reasoning_effort));
    setCw(String(next.context_window_k));
    setMaxTokens(String(next.max_tokens));
    setMaxTokensField(fromStore(next.max_tokens_field));
    setModels([]);
    setModelsOpen(false);
  }

  // Local or self-hosted OpenAI-compatible server: Chat Completions, no reasoning fields, an explicit output limit.
  function applyLocal() {
    if (!isNew) return;
    setVendor("");
    setPreset("");
    if (!name.trim() || Object.values(LLM_PROFILE_PRESET_NAMES).includes(name)) setName(LOCAL_PROFILE_NAME);
    if (!apiKey.trim()) setApiKey(LOCAL_PLACEHOLDER_KEY);
    setFormat("openai");
    setModel("");
    setBaseUrl("");
    setThinkingType(NONE);
    setEffort(NONE);
    setCw(LOCAL_CONTEXT_WINDOW_K);
    setMaxTokens(LOCAL_OUTPUT_LIMIT);
    setMaxTokensField(NONE);
    setModels([]);
    setModelsOpen(false);
  }

  // Switching the connection type; the ChatGPT sign-in fixes the transport fields.
  function changeAuthMethod(v: "api" | "subscription") {
    setUseSubscription(v === "subscription");
    setModels([]);
    setSubscriptionModels([]);
    setApiKey("");
    if (v === "subscription") {
      setFormat("openai-responses");
      setBaseUrl("https://api.openai.com/v1");
      setStreaming(true);
      setThinkingType(NONE);
      setMaxTokens("0");
      setProxy("");
      setCyberProgram("standard");
      setSessionHeaderKey("");
    }
  }

  // Hand-off from the guided flow to the existing ChatGPT / Codex sign-in form.
  function chooseCodex() {
    changeAuthMethod("subscription");
    setGuided(false);
  }

  // Back from the sign-in form of a new profile to the guided choices.
  function backToGuided() {
    changeAuthMethod("api");
    setFormat("anthropic");
    setBaseUrl("");
    setModel("");
    setPreset("");
    setVendor("");
    setGuided(true);
  }

  const profileId = profile ? Number(profile.id) : undefined;
  const endpointWarnings = llmEndpointWarnings(format, baseUrl, model);
  const testSig = JSON.stringify([
    format,
    model.trim(),
    baseUrl.trim(),
    apiKey,
    proxy.trim(),
    thinkingType,
    effort,
    streaming,
    sessionHeaderKey.trim(),
  ]);
  const testPassed = !!tested && tested.ok && tested.sig === testSig;

  async function loadModels() {
    if (useSubscription && !subscriptionId) {
      toast.error(swt("providers.subscriptionSignInFirst"));
      return;
    }
    if (loadingModels) return;
    setLoadingModels(true);
    setModels([]);
    try {
      const r = await api.fetchLLMModels(
        format,
        baseUrl,
        apiKey,
        proxy,
        useSubscription ? undefined : profileId,
        useSubscription ? subscriptionId : "",
      );
      if (r.ok && r.models && r.models.length > 0) {
        setModels(r.models);
        setModelsOpen(true);
        toast.success(swt("interface.m1370", { p0: r.models.length }));
      } else {
        toast.error(swt("interface.m1371", { p0: r.error ?? swt("interface.m1372") }));
      }
    } catch (e) {
      toast.error(swt("interface.m1373", { p0: (e as Error).message }));
    } finally {
      setLoadingModels(false);
    }
  }

  async function testConnection() {
    if (useSubscription && !subscriptionId) {
      toast.error(swt("providers.subscriptionSignInFirst"));
      return;
    }
    if (testing) return;
    setTesting(true);
    const sig = testSig;
    try {
      // Test using the profile's actual reasoning parameters so unsupported fields fail here,
      // not during tasks. Pass profile ID to use the stored key when the key input is blank.
      const r = await api.testLLM(
        format,
        model,
        baseUrl,
        apiKey,
        useSubscription ? "" : proxy,
        useSubscription ? "" : toStore(thinkingType),
        toStore(effort),
        useSubscription ? undefined : profileId,
        streaming,
        useSubscription ? "" : sessionHeaderKey.trim(),
        useSubscription ? subscriptionId : "",
        useSubscription ? cyberProgram : "standard",
      );
      // Show response content to prove the model actually answered as it would in a conversation.
      if (r.ok) {
        const message = swt("interface.m1374", { p0: r.latency_ms ?? "?", p1: r.model ?? model });
        const detail = r.reply ? swt("interface.m1375", { p0: r.reply }) : "";
        setTested({ sig, ok: true, message, detail });
        if (!guided) toast.success(message, { description: detail || undefined });
      } else {
        const message = swt("interface.m1376", { p0: r.error ?? swt("interface.m1377") });
        setTested({ sig, ok: false, message, detail: "" });
        if (!guided) toast.error(message);
      }
    } catch (e) {
      const message = swt("interface.m1378", { p0: (e as Error).message });
      setTested({ sig, ok: false, message, detail: "" });
      if (!guided) toast.error(message);
    } finally {
      setTesting(false);
    }
  }

  // Returns the saved profile id, or null when validation or the request failed.
  // stay keeps the editor open so the guided flow can show its closing step.
  async function save(stay = false): Promise<string | null> {
    if (!name.trim() || !model.trim()) {
      toast.error(swt("interface.m1379"));
      return null;
    }
    if (useSubscription && !subscriptionId) {
      toast.error(swt("providers.subscriptionSignInFirst"));
      return null;
    }
    const prices = [priceInput, priceCachedInput, priceOutput].map((v) => v.trim());
    if (prices.some((v) => v !== "" && !(Number.isFinite(Number(v)) && Number(v) >= 0))) {
      toast.error(swt("budget.priceInvalid"));
      return null;
    }
    if (priceCurrency.trim() !== "" && !/^[A-Za-z]{3}$/.test(priceCurrency.trim())) {
      toast.error(swt("budget.invalidCurrency"));
      return null;
    }
    if (saving) return null;
    setSaving(true);
    try {
      const { id } = await api.saveLLMProfile({
        ...(profile ? { id: Number(profile.id) } : {}),
        name: name.trim(),
        format,
        model: model.trim(),
        base_url: baseUrl.trim(),
        proxy: useSubscription ? "" : proxy.trim(),
        api_key: useSubscription ? "" : apiKey,
        subscription_id: useSubscription ? subscriptionId : "",
        cyber_access_program: useSubscription ? cyberProgram : "",
        rate_per_second: Number(rps) || 0,
        rate_per_minute: Number(rpm) || 0,
        context_window_k: Number(cw) || 0,
        thinking_type: useSubscription ? "" : toStore(thinkingType),
        reasoning_effort: toStore(effort),
        priority: Number(priority) || 0,
        pool_exclude: poolExclude,
        streaming,
        max_tokens: useSubscription ? 0 : Math.max(0, Number(maxTokens) || 0),
        // Output-field selection affects only OpenAI Chat Completions; other formats use defaults.
        // The backend normalizes again; this also prevents contradictory UI submissions.
        max_tokens_field: format === "openai" ? toStore(maxTokensField) : "",
        session_header_key: useSubscription ? "" : sessionHeaderKey.trim(),
        retry,
        // Any price key makes the server save all four, so they are sent only when the user has set or
        // changed something; untouched or never-priced profiles keep what is stored.
        ...(priceChanged(profile, { priceInput, priceCachedInput, priceOutput, priceCurrency })
          ? {
              price_input_per_million: priceValue(priceInput),
              price_cached_input_per_million: priceValue(priceCachedInput),
              price_output_per_million: priceValue(priceOutput),
              price_currency: priceCurrency.trim().toUpperCase() || "USD",
            }
          : {}),
      });
      if (isNew) {
        if (!stay) toast.success(swt("interface.m1380", { p0: name.trim() }));
      } else toast.success(profile?.is_default ? swt("interface.m1381") : swt("interface.m0377"));
      onSaved(String(id));
      if (!stay) onOpenChange(false);
      return String(id);
    } catch (e) {
      toast.error(swt("interface.m0267", { p0: (e as Error).message }));
      return null;
    } finally {
      setSaving(false);
    }
  }

  return {
    profile,
    isNew,
    open,
    guided,
    close: () => onOpenChange(false),
    onSaved,
    useSubscription,
    subscriptionId,
    setSubscriptionId,
    cyberProgram,
    setCyberProgram,
    preset,
    vendor,
    region,
    name,
    setName,
    format,
    setFormat,
    model,
    setModel,
    baseUrl,
    setBaseUrl,
    proxy,
    setProxy,
    apiKey,
    setApiKey,
    keyHint,
    rps,
    setRps,
    rpm,
    setRpm,
    cw,
    setCw,
    thinkingType,
    setThinkingType,
    effort,
    setEffort,
    priority,
    setPriority,
    poolExclude,
    setPoolExclude,
    streaming,
    setStreaming,
    maxTokens,
    setMaxTokens,
    maxTokensField,
    setMaxTokensField,
    sessionHeaderKey,
    setSessionHeaderKey,
    retry,
    setRetry,
    priceInput,
    setPriceInput,
    priceCachedInput,
    setPriceCachedInput,
    priceOutput,
    setPriceOutput,
    priceCurrency,
    setPriceCurrency,
    testing,
    saving,
    models,
    setModels,
    subscriptionModels,
    setSubscriptionModels,
    bumpSubscriptionModels: () => setSubscriptionModelsRefresh((count) => count + 1),
    loadingModels,
    modelsOpen,
    setModelsOpen,
    tested,
    testPassed,
    endpointWarnings,
    applyVendor,
    applyRegion,
    applyPreset,
    applyLocal,
    changeAuthMethod,
    chooseCodex,
    backToGuided,
    loadModels,
    testConnection,
    save,
  };
}

export type ProfileDraft = ReturnType<typeof useProfileDraft>;
