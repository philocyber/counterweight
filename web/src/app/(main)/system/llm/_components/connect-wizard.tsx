"use client";

import * as React from "react";

import { CheckCircle2Icon, ChevronDownIcon, Loader2Icon, PlugZapIcon, SaveIcon, XCircleIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { type MessageKey, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { BEDROCK_REGIONS, LLM_VENDORS, type LLMProfilePresetId, type LLMVendorId } from "@/lib/llm-profile-presets";
import { cn } from "@/lib/utils";

import type { ProfileDraft } from "./profile-draft";
import { EndpointWarnings, FormatSelect, LocalLimitFields, ModelPicker, ProfileAdvancedFields } from "./profile-fields";

type Choice = LLMVendorId | "local";

const VENDOR_LABEL: Record<LLMVendorId, MessageKey> = {
  anthropic: "providers.vendorAnthropic",
  openai: "providers.vendorOpenai",
  zai: "providers.vendorZai",
  vercel: "providers.vendorVercel",
  bedrock: "providers.vendorBedrock",
};

const PRESET_LABEL: Record<LLMProfilePresetId, MessageKey> = {
  "claude-opus-5-5": "providers.claudeOpus",
  "claude-fable-5-1": "providers.claudeFable",
  "claude-sonnet-5-5": "providers.claudeSonnet",
  "claude-haiku-5-5": "providers.claudeHaiku",
  "openai-gpt-6-1-sol": "providers.openaiSol",
  "openai-gpt-6-luna": "providers.openaiLuna",
  "zai-general": "providers.general",
  "zai-coding-reference": "providers.coding",
  "vercel-gateway-chat": "providers.vercelChat",
  "vercel-gateway-claude": "providers.vercelClaude",
  "bedrock-claude": "providers.bedrockClaude",
  "bedrock-open-models": "providers.bedrockOpen",
};

const CHOICE_NAME: Record<Choice, MessageKey> = {
  anthropic: "models.choiceAnthropic",
  openai: "models.choiceOpenai",
  zai: "models.choiceZai",
  vercel: "models.choiceVercel",
  bedrock: "models.choiceBedrock",
  local: "models.choiceLocal",
};

const GROUPS: { label: MessageKey; leaves: MessageKey; choices: Choice[] }[] = [
  { label: "models.groupHosted", leaves: "models.leavesHosted", choices: ["anthropic", "openai", "zai"] },
  { label: "models.groupGateway", leaves: "models.leavesGateway", choices: ["vercel", "bedrock"] },
  { label: "models.groupLocal", leaves: "models.leavesLocal", choices: ["local"] },
];

const LOCAL_SERVERS: { name: string; url: string }[] = [
  { name: "Ollama", url: "http://localhost:11434/v1" },
  { name: "LM Studio", url: "http://localhost:1234/v1" },
  { name: "llama.cpp", url: "http://localhost:8080/v1" },
];

// Provider-specific guidance for the selected template: what it assumes and where the official docs are.
function PresetHelp({ preset }: { preset: LLMProfilePresetId | "" }) {
  const { t } = useI18n();
  const link = (href: string, label: string) => (
    <a className="underline underline-offset-4" href={href} target="_blank" rel="noreferrer">
      {label}
    </a>
  );
  if (preset.startsWith("claude-")) {
    return (
      <>
        <p className="text-muted-foreground text-xs">{t("providers.claudeHelp")}</p>
        <div className="flex flex-wrap gap-3 text-xs">
          {link(
            `https://platform.claude.com/docs/en/models/${preset.replace("claude-", "")}/overview`,
            t("providers.claudeOfficial"),
          )}
        </div>
      </>
    );
  }
  if (preset.startsWith("openai-")) {
    return (
      <>
        <p className="text-muted-foreground text-xs">{t("providers.openaiHelp")}</p>
        <div className="flex flex-wrap gap-3 text-xs">
          {link(
            `https://developers.openai.com/api/docs/models/${preset === "openai-gpt-6-1-sol" ? "gpt-6.1-sol" : "gpt-6-luna"}`,
            t("providers.openaiOfficial"),
          )}
        </div>
      </>
    );
  }
  if (preset.startsWith("vercel-") || preset.startsWith("bedrock-")) {
    const vercel = preset.startsWith("vercel-");
    return (
      <>
        <p className="text-muted-foreground text-xs">
          {t(
            vercel
              ? "providers.vercelHelp"
              : preset === "bedrock-claude"
                ? "providers.bedrockClaudeHelp"
                : "providers.bedrockOpenHelp",
          )}
        </p>
        <div className="flex flex-wrap gap-3 text-xs">
          {link(
            vercel
              ? "https://vercel.com/docs/ai-gateway/authentication-and-byok"
              : "https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html",
            t(vercel ? "providers.vercelDocs" : "providers.bedrockDocs"),
          )}
        </div>
      </>
    );
  }
  if (preset.startsWith("zai-")) {
    return (
      <>
        <p
          className={
            preset === "zai-coding-reference"
              ? "text-amber-700 text-xs dark:text-amber-400"
              : "text-muted-foreground text-xs"
          }
        >
          {t(preset === "zai-coding-reference" ? "providers.codingHelp" : "providers.generalHelp")}
        </p>
        <p className="text-muted-foreground text-xs">{t("providers.modelHelp")}</p>
        <div className="flex flex-wrap gap-3 text-xs">
          {link("https://docs.z.ai/guides/llm/glm-5.3", t("providers.officialModel"))}
          {link("https://docs.z.ai/devpack/usage-policy", t("providers.officialPolicy"))}
        </div>
      </>
    );
  }
  return null;
}

// Guided "Connect a model" flow for a new profile: where the model runs, the minimum fields, then test and save.
// It edits the same draft as the full form, so payloads and field semantics do not change.
export function ConnectWizard({ d, hasActiveProfile }: { d: ProfileDraft; hasActiveProfile: boolean }) {
  "use no memo";
  const { t } = useI18n();
  const [step, setStep] = React.useState<1 | 2 | 3>(1);
  const [choice, setChoice] = React.useState<Choice | null>(null);
  const [skipTest, setSkipTest] = React.useState(false);
  const [advancedOpen, setAdvancedOpen] = React.useState(false);
  // Set once the profile is saved: whether it was activated automatically, and the pending "make active" answer.
  const [saved, setSaved] = React.useState<{
    id: string;
    auto: boolean;
    activated: boolean;
    activationFailed: boolean;
  } | null>(null);
  const [makeActive, setMakeActive] = React.useState(false);
  const [finishing, setFinishing] = React.useState(false);

  const local = choice === "local";
  const vendor = choice && choice !== "local" ? choice : null;
  const presets = vendor ? LLM_VENDORS[vendor].presets : [];
  const detailsOk = !!d.name.trim() && !!d.model.trim() && !!d.apiKey.trim() && (!local || !!d.baseUrl.trim());
  const canSave = d.testPassed || skipTest;

  function pick(next: Choice) {
    // Re-picking the same choice after going back keeps whatever the user already edited.
    if (next !== choice) {
      if (next === "local") d.applyLocal();
      else d.applyVendor(next);
      setChoice(next);
      setSkipTest(false);
    }
    setStep(2);
  }

  async function saveProfile() {
    const auto = !hasActiveProfile;
    const id = await d.save(true);
    if (!id) return;
    let activated = false;
    let activationFailed = false;
    if (auto) {
      try {
        await api.activateLLMProfile(id);
        toast.success(t("interface.m1419", { p0: d.name.trim() }));
        d.onSaved(id);
        activated = true;
      } catch (e) {
        activationFailed = true;
        toast.error(t("interface.m1420", { p0: (e as Error).message }));
      }
    }
    setSaved({ id, auto, activated, activationFailed });
  }

  async function finish() {
    if (saved && makeActive) {
      setFinishing(true);
      try {
        await api.activateLLMProfile(saved.id);
        toast.success(t("interface.m1419", { p0: d.name.trim() }));
        d.onSaved(saved.id);
      } catch (e) {
        toast.error(t("interface.m1420", { p0: (e as Error).message }));
        setFinishing(false);
        return;
      }
      setFinishing(false);
    }
    d.close();
  }

  if (saved) {
    return (
      <>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <div className="flex items-start gap-2 rounded-lg border p-3">
            <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
            <div className="grid gap-1 text-sm">
              <p className="font-medium">{t("models.savedTitle", { p0: d.name.trim() })}</p>
              {saved.activated && <p className="text-muted-foreground text-xs">{t("models.savedActivated")}</p>}
              {saved.activationFailed && (
                <p className="text-amber-700 text-xs dark:text-amber-400">{t("models.savedActivationFailed")}</p>
              )}
            </div>
          </div>
          {!saved.auto && (
            <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
              <div className="grid gap-0.5">
                <Label htmlFor="p-make-active" className="text-sm">
                  {t("models.makeActiveQuestion")}
                </Label>
                <p className="text-muted-foreground text-xs">{t("models.makeActiveHelp")}</p>
              </div>
              <Switch
                id="p-make-active"
                checked={makeActive}
                onCheckedChange={setMakeActive}
                aria-label={t("models.makeActiveQuestion")}
              />
            </div>
          )}
        </div>
        <div className="flex gap-2 border-t px-4 py-3">
          <Button className="flex-1" onClick={() => void finish()} disabled={finishing}>
            {finishing && <Loader2Icon className="animate-spin" />}
            {t("models.done")}
          </Button>
        </div>
      </>
    );
  }

  const stepNames = [t("models.stepWhere"), t("models.stepDetails"), t("models.stepTest")];

  return (
    <>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground text-xs">
          {t("models.stepLabel", { p0: step, p1: 3 })} · {stepNames[step - 1]}
        </p>

        {step === 1 && (
          <>
            <p className="text-muted-foreground text-xs">{t("models.dataNote")}</p>
            {GROUPS.map((group) => (
              <section key={group.label} className="grid gap-2">
                <h3 className="font-medium text-sm">{t(group.label)}</h3>
                <div className="grid gap-2">
                  {group.choices.map((c) => (
                    <button
                      key={c}
                      type="button"
                      onClick={() => pick(c)}
                      className={cn(
                        "grid gap-0.5 rounded-lg border p-3 text-left outline-none transition-colors hover:border-foreground/30 focus-visible:ring-2 focus-visible:ring-ring",
                        choice === c && "border-foreground/40 bg-muted/30",
                      )}
                    >
                      <span className="font-medium text-sm">{t(CHOICE_NAME[c])}</span>
                      {c === "local" && (
                        <span className="text-muted-foreground text-xs">{t("models.choiceLocalHint")}</span>
                      )}
                      <span className="text-muted-foreground text-xs">
                        {c === "local" ? t(group.leaves) : t(group.leaves, { p0: t(CHOICE_NAME[c]) })}
                      </span>
                    </button>
                  ))}
                </div>
              </section>
            ))}
            <section className="grid gap-2">
              <h3 className="font-medium text-sm">{t("models.groupSubscription")}</h3>
              <button
                type="button"
                onClick={d.chooseCodex}
                className="grid gap-0.5 rounded-lg border p-3 text-left outline-none transition-colors hover:border-foreground/30 focus-visible:ring-2 focus-visible:ring-ring"
              >
                <span className="font-medium text-sm">{t("models.choiceCodex")}</span>
                <span className="text-muted-foreground text-xs">{t("models.choiceCodexHint")}</span>
                <span className="text-muted-foreground text-xs">{t("models.leavesCodex")}</span>
              </button>
            </section>
          </>
        )}

        {step === 2 && choice && (
          <>
            <div className="grid gap-1 rounded-md border bg-muted/20 p-3">
              <p className="font-medium text-sm">{t(CHOICE_NAME[choice])}</p>
              <p className="text-muted-foreground text-xs">
                {local
                  ? t("models.leavesLocal")
                  : t(vendor === "vercel" || vendor === "bedrock" ? "models.leavesGateway" : "models.leavesHosted", {
                      p0: t(CHOICE_NAME[choice]),
                    })}
              </p>
            </div>

            {vendor && (
              <div className="grid gap-2">
                {presets.length > 1 && (
                  <>
                    <Label htmlFor="p-preset">{t("providers.template")}</Label>
                    <Select value={d.preset} onValueChange={(id) => d.applyPreset(id)}>
                      <SelectTrigger id="p-preset" className="w-full min-w-0">
                        <SelectValue placeholder={t("providers.chooseTemplate")} />
                      </SelectTrigger>
                      <SelectContent>
                        {presets.map((id) => (
                          <SelectItem key={id} value={id}>
                            {t(PRESET_LABEL[id])}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </>
                )}
                {vendor === "bedrock" && (
                  <>
                    <Label htmlFor="p-region">{t("providers.region")}</Label>
                    <Select value={d.region} onValueChange={d.applyRegion}>
                      <SelectTrigger id="p-region" className="w-full min-w-0">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {BEDROCK_REGIONS.map((r) => (
                          <SelectItem key={r} value={r}>
                            {r}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <p className="text-muted-foreground text-xs">{t("providers.regionHelp")}</p>
                  </>
                )}
                <PresetHelp preset={d.preset} />
              </div>
            )}

            <div className="grid gap-2">
              <Label htmlFor="p-name">{t("interface.m0868")}</Label>
              <Input
                id="p-name"
                placeholder={t("interface.m1387")}
                value={d.name}
                onChange={(e) => d.setName(e.target.value)}
              />
            </div>

            {local && (
              <div className="grid gap-2">
                <Label htmlFor="p-base-url">{t("models.serverAddress")}</Label>
                <Input
                  id="p-base-url"
                  className="font-mono"
                  placeholder="http://localhost:11434/v1"
                  value={d.baseUrl}
                  onChange={(e) => d.setBaseUrl(e.target.value)}
                />
                <div className="flex flex-wrap gap-2">
                  {LOCAL_SERVERS.map((s) => (
                    <Button
                      key={s.url}
                      type="button"
                      size="sm"
                      variant={d.baseUrl.trim() === s.url ? "secondary" : "outline"}
                      title={s.url}
                      onClick={() => d.setBaseUrl(s.url)}
                    >
                      {s.name}
                    </Button>
                  ))}
                </div>
                <p className="text-muted-foreground text-xs">{t("models.serverAddressHelp")}</p>
                <p className="text-muted-foreground text-xs">{t("models.dockerNote")}</p>
              </div>
            )}

            <div className="grid gap-2">
              <Label htmlFor="p-api-key">{t("english.e100")}</Label>
              <Input
                id="p-api-key"
                type={local ? "text" : "password"}
                placeholder={
                  vendor === "bedrock"
                    ? t("providers.keyBedrock")
                    : vendor === "vercel"
                      ? t("providers.keyVercel")
                      : "sk-…"
                }
                value={d.apiKey}
                onChange={(e) => d.setApiKey(e.target.value)}
              />
              {local ? (
                <p className="text-muted-foreground text-xs">{t("models.localKeyHelp")}</p>
              ) : (
                vendor && (
                  <a
                    className="w-fit text-xs underline underline-offset-4"
                    href={LLM_VENDORS[vendor].keyUrl}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {t("providers.getKey", { p0: t(VENDOR_LABEL[vendor]) })}
                  </a>
                )
              )}
            </div>

            <div className="grid gap-2">
              <Label htmlFor="p-model">{t("interface.m0627")}</Label>
              <ModelPicker d={d} labeled placeholder={local ? t("models.localModelPlaceholder") : "claude-opus-5-5"} />
              <p className="text-muted-foreground text-xs">{t("models.modelHelp")}</p>
              <EndpointWarnings d={d} />
            </div>
            {!detailsOk && (
              <p className="text-muted-foreground text-xs">
                {t(local ? "models.detailsIncompleteLocal" : "models.detailsIncomplete")}
              </p>
            )}
          </>
        )}

        {step === 3 && choice && (
          <>
            <div className="grid gap-0.5 rounded-md border bg-muted/20 p-3 text-sm">
              <span className="truncate font-medium">{d.name.trim()}</span>
              <code className="truncate font-mono text-muted-foreground text-xs">{d.model.trim()}</code>
            </div>

            <div className="grid gap-2 rounded-lg border p-3">
              <p className="text-muted-foreground text-xs">{t("models.testHelp")}</p>
              <Button type="button" variant="outline" className="w-fit" onClick={d.testConnection} disabled={d.testing}>
                {d.testing ? <Loader2Icon className="animate-spin" /> : <PlugZapIcon />}
                {d.testing ? t("interface.m1417") : t("interface.m1418")}
              </Button>
              {d.tested && !d.testing && (
                <div
                  role="status"
                  className={cn(
                    "flex items-start gap-2 text-xs",
                    d.testPassed ? "text-emerald-700 dark:text-emerald-400" : "text-destructive",
                  )}
                >
                  {d.testPassed ? (
                    <CheckCircle2Icon className="mt-0.5 size-4 shrink-0" />
                  ) : (
                    <XCircleIcon className="mt-0.5 size-4 shrink-0" />
                  )}
                  <div className="min-w-0 break-words">
                    <p>{d.tested.ok && !d.testPassed ? t("models.testStale") : d.tested.message}</p>
                    {d.testPassed && d.tested.detail && <p className="text-muted-foreground">{d.tested.detail}</p>}
                  </div>
                </div>
              )}
              {d.tested && !d.tested.ok && <EndpointWarnings d={d} />}
              <div className="flex items-start gap-2 border-t pt-3">
                <Checkbox
                  id="p-skip-test"
                  checked={skipTest}
                  onCheckedChange={(v) => setSkipTest(v === true)}
                  className="mt-0.5"
                />
                <div className="grid gap-0.5">
                  <Label htmlFor="p-skip-test" className="text-sm">
                    {t("models.skipTest")}
                  </Label>
                  <p className="text-muted-foreground text-xs">{t("models.skipTestHelp")}</p>
                </div>
              </div>
            </div>

            {local && <LocalLimitFields d={d} />}

            <Collapsible open={advancedOpen} onOpenChange={setAdvancedOpen} className="grid gap-4">
              <CollapsibleTrigger asChild>
                <Button type="button" variant="ghost" className="w-fit px-2">
                  <ChevronDownIcon className={cn("transition-transform", advancedOpen && "rotate-180")} />
                  {t("models.advancedTitle")}
                </Button>
              </CollapsibleTrigger>
              <CollapsibleContent className="grid gap-4">
                <p className="-mt-2 text-muted-foreground text-xs">{t("models.advancedHelp")}</p>
                <FormatSelect d={d} />
                {!local && (
                  <div className="grid gap-2">
                    <Label htmlFor="p-base-url">{t("interface.m1391")}</Label>
                    <Input
                      id="p-base-url"
                      className="font-mono"
                      placeholder={d.format === "anthropic" ? "https://api.anthropic.com" : "https://api.openai.com/v1"}
                      value={d.baseUrl}
                      onChange={(e) => d.setBaseUrl(e.target.value)}
                    />
                    <EndpointWarnings d={d} />
                  </div>
                )}
                <ProfileAdvancedFields d={d} skipLimits={local} />
              </CollapsibleContent>
            </Collapsible>
          </>
        )}
      </div>

      {step > 1 && (
        <div className="flex gap-2 border-t px-4 py-3">
          <Button variant="outline" onClick={() => setStep((s) => (s - 1) as 1 | 2)}>
            {t("models.back")}
          </Button>
          {step === 2 ? (
            <Button className="flex-1" disabled={!detailsOk} onClick={() => setStep(3)}>
              {t("models.continue")}
            </Button>
          ) : (
            <Button className="flex-1" disabled={!canSave || d.saving} onClick={() => void saveProfile()}>
              {d.saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
              {t("models.saveProfile")}
            </Button>
          )}
        </div>
      )}
    </>
  );
}
