"use client";

import { Loader2Icon, RefreshCwIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";

import { NONE, type ProfileDraft } from "./profile-draft";
import { ProfileRetryFields } from "./retry";

const THINKING_TYPES: { value: string; label: string }[] = [
  {
    value: NONE,
    get label() {
      return swt("interface.m1329");
    },
  },
  {
    value: "disabled",
    get label() {
      return swt("interface.m0211");
    },
  },
  {
    value: "enabled",
    get label() {
      return swt("interface.m0210");
    },
  },
];
// Output-limit request key matters only for OpenAI format; reuse NONE/empty-string conversion.
const MAX_TOKENS_FIELDS: { value: string; label: string }[] = [
  {
    value: NONE,
    get label() {
      return swt("interface.m1330");
    },
  },
  { value: "max_completion_tokens", label: "max_completion_tokens" },
];
// The other formats have fixed field names; explain that this selection does not affect them.
const MAX_TOKENS_FIELD_HINTS: Record<string, string> = {
  get openai() {
    return swt("interface.m1331");
  },
  get anthropic() {
    return swt("interface.m1332");
  },
  get "openai-responses"() {
    return swt("interface.m1333");
  },
};
const EFFORT_LEVELS: { value: string; label: string }[] = [
  {
    value: NONE,
    get label() {
      return swt("interface.m1329");
    },
  },
  {
    value: "low",
    get label() {
      return swt("english.e089");
    },
  },
  {
    value: "medium",
    get label() {
      return swt("english.e090");
    },
  },
  {
    value: "high",
    get label() {
      return swt("english.e091");
    },
  },
  {
    value: "xhigh",
    get label() {
      return swt("english.e092");
    },
  },
  {
    value: "max",
    get label() {
      return swt("english.e093");
    },
  },
];

// Price inputs as strings, the way the draft keeps every number field. The fields below render only
// once the draft carries this state, so a draft without price support shows nothing.
export interface ProfilePriceState {
  priceInput: string;
  setPriceInput: (value: string) => void;
  priceCachedInput: string;
  setPriceCachedInput: (value: string) => void;
  priceOutput: string;
  setPriceOutput: (value: string) => void;
  priceCurrency: string;
  setPriceCurrency: (value: string) => void;
}

function hasPriceState(d: object): d is ProfilePriceState {
  return typeof (d as Partial<ProfilePriceState>).setPriceInput === "function";
}

// Optional per-million-token prices. They only feed cost estimates and cost budgets; nothing is built in.
function ProfilePriceFields({ d }: { d: ProfilePriceState }) {
  const { t } = useI18n();
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">{t("budget.pricesTitle")}</Label>
        <p className="text-muted-foreground text-xs">{t("budget.pricesNote")}</p>
      </div>
      <div className="grid gap-4 sm:grid-cols-4">
        <div className="grid gap-2">
          <Label htmlFor="p-price-input">{t("budget.priceInput")}</Label>
          <Input
            id="p-price-input"
            type="number"
            min={0}
            step="any"
            value={d.priceInput}
            onChange={(e) => d.setPriceInput(e.target.value)}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="p-price-cached">{t("budget.priceCachedInput")}</Label>
          <Input
            id="p-price-cached"
            type="number"
            min={0}
            step="any"
            value={d.priceCachedInput}
            onChange={(e) => d.setPriceCachedInput(e.target.value)}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="p-price-output">{t("budget.priceOutput")}</Label>
          <Input
            id="p-price-output"
            type="number"
            min={0}
            step="any"
            value={d.priceOutput}
            onChange={(e) => d.setPriceOutput(e.target.value)}
          />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="p-price-currency">{t("budget.priceCurrency")}</Label>
          <Input
            id="p-price-currency"
            maxLength={3}
            className="uppercase"
            placeholder="USD"
            value={d.priceCurrency}
            onChange={(e) => d.setPriceCurrency(e.target.value)}
          />
        </div>
      </div>
    </div>
  );
}

// Pre-flight warnings for the endpoint path, Region placeholder and model id style. Advisory only.
export function EndpointWarnings({ d }: { d: ProfileDraft }) {
  const { t } = useI18n();
  return d.endpointWarnings.map((w) => (
    <p key={w} role="alert" className="text-xs text-amber-700 dark:text-amber-400">
      {t(`providers.${w}`)}
    </p>
  ));
}

// Model input for API-key profiles with the "load models" call; labeled shows a text button instead of an icon.
export function ModelPicker({
  d,
  labeled = false,
  placeholder = "claude-opus-5-5",
}: {
  d: ProfileDraft;
  labeled?: boolean;
  placeholder?: string;
}) {
  const { t } = useI18n();
  return (
    <div className="flex gap-2">
      <Input
        id="p-model"
        className="font-mono"
        placeholder={placeholder}
        value={d.model}
        onChange={(e) => d.setModel(e.target.value)}
      />
      {/* This body-portaled Popover sits outside Sheet's scroll lock. modal gives it the top scroll lock so its list can scroll. */}
      <Popover open={d.modelsOpen} onOpenChange={d.setModelsOpen} modal>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size={labeled ? "default" : "icon"}
            className="shrink-0"
            disabled={d.loadingModels}
            onClick={d.loadModels}
            title={t("interface.m1390")}
          >
            {d.loadingModels ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
            {labeled && t("models.loadModels")}
          </Button>
        </PopoverTrigger>
        {d.models.length > 0 && (
          <PopoverContent className="max-h-72 w-72 gap-0 overflow-y-auto overscroll-contain p-1" align="end">
            {d.models.map((m) => (
              <button
                key={m}
                type="button"
                className="w-full shrink-0 rounded-md px-2 py-1.5 text-left font-mono text-xs hover:bg-accent hover:text-accent-foreground"
                onClick={() => {
                  d.setModel(m);
                  d.setModelsOpen(false);
                }}
              >
                {m}
              </button>
            ))}
          </PopoverContent>
        )}
      </Popover>
    </div>
  );
}

// Context window and output limit as they appear for self-hosted servers, with the reason each one matters.
export function LocalLimitFields({ d }: { d: ProfileDraft }) {
  const { t } = useI18n();
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-2">
        <Label htmlFor="p-cw">{t("interface.m1402")}</Label>
        <Input
          id="p-cw"
          type="number"
          min={0}
          max={1000}
          value={d.cw}
          onChange={(e) => d.setCw(e.target.value)}
          placeholder="200"
        />
        <p className="text-muted-foreground text-xs">{t("models.localContextHelp")}</p>
      </div>
      <div className="grid gap-2 border-t pt-3">
        <Label htmlFor="p-max-tokens">{t("interface.m1410")}</Label>
        <Input
          id="p-max-tokens"
          type="number"
          min={0}
          value={d.maxTokens}
          onChange={(e) => d.setMaxTokens(e.target.value)}
          placeholder="0"
        />
        <p className="text-muted-foreground text-xs">{t("models.localOutputHelp")}</p>
      </div>
    </div>
  );
}

// Everything beyond the connection basics: proxy, session header, rate limits, rotation, streaming, output limit,
// reasoning and retry. skipLimits hides the context window and output limit when they are shown elsewhere.
export function ProfileAdvancedFields({ d, skipLimits = false }: { d: ProfileDraft; skipLimits?: boolean }) {
  const { t } = useI18n();
  const { useSubscription } = d;
  return (
    <>
      {!useSubscription && (
        <>
          <div className="grid gap-2">
            <Label htmlFor="p-proxy">{t("interface.m1392")}</Label>
            <Input
              id="p-proxy"
              className="font-mono"
              placeholder="socks5://user:pass@127.0.0.1:1080 · http://127.0.0.1:8080"
              value={d.proxy}
              onChange={(e) => d.setProxy(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">{t("interface.m1393")}</p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-session-header">{t("interface.m1394")}</Label>
            <Input
              id="p-session-header"
              className="font-mono"
              placeholder={t("interface.m1395")}
              value={d.sessionHeaderKey}
              onChange={(e) => d.setSessionHeaderKey(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              {t("interface.m1396")}
              <b>{t("interface.m1397")}</b>
              {t("interface.m1398")}
            </p>
          </div>
        </>
      )}

      <div className={skipLimits ? "grid gap-4 sm:grid-cols-2" : "grid gap-4 sm:grid-cols-3"}>
        <div className="grid gap-2">
          <Label htmlFor="p-rps">{t("interface.m1400")}</Label>
          <Input id="p-rps" type="number" min={0} value={d.rps} onChange={(e) => d.setRps(e.target.value)} />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="p-rpm">{t("interface.m1401")}</Label>
          <Input id="p-rpm" type="number" min={0} value={d.rpm} onChange={(e) => d.setRpm(e.target.value)} />
        </div>
        {!skipLimits && (
          <div className="grid gap-2">
            <Label htmlFor="p-cw">{t("interface.m1402")}</Label>
            <Input
              id="p-cw"
              type="number"
              min={0}
              max={1000}
              value={d.cw}
              onChange={(e) => d.setCw(e.target.value)}
              placeholder="200"
            />
          </div>
        )}
      </div>
      <p className="-mt-2 text-muted-foreground text-xs">{t("interface.m1403")}</p>
      {!skipLimits && (Number(d.cw) || 0) === 0 && (
        <p className="-mt-2 text-amber-700 text-xs dark:text-amber-400">{t("providers.cwUnset")}</p>
      )}

      <div className="grid gap-3 rounded-lg border p-3">
        <div className="flex items-center justify-between gap-4">
          <div className="grid gap-0.5">
            <Label htmlFor="p-priority" className="text-sm">
              {t("interface.m1404")}
            </Label>
            <p className="text-muted-foreground text-xs">{t("interface.m1405")}</p>
          </div>
          <Input
            id="p-priority"
            type="number"
            className="w-24 shrink-0"
            value={d.priority}
            onChange={(e) => d.setPriority(e.target.value)}
          />
        </div>
        <div className="flex items-center justify-between gap-4 border-t pt-3">
          <div className="grid gap-0.5">
            <Label className="text-sm">{t("interface.m1362")}</Label>
            <p className="text-muted-foreground text-xs">{t("interface.m1406")}</p>
          </div>
          <Switch checked={d.poolExclude} onCheckedChange={d.setPoolExclude} aria-label={t("interface.m1362")} />
        </div>
        <div className="flex items-center justify-between gap-4 border-t pt-3">
          <div className="grid gap-0.5">
            <Label className="text-sm">{t("interface.m1407")}</Label>
            <p className="text-muted-foreground text-xs">{t("interface.m1408")}</p>
          </div>
          <Switch
            disabled={useSubscription}
            checked={d.streaming}
            onCheckedChange={d.setStreaming}
            aria-label={t("interface.m1409")}
          />
        </div>
      </div>

      <div className="grid gap-3 rounded-lg border p-3">
        {!skipLimits && (
          <div className="flex items-center justify-between gap-4">
            <div className="grid gap-0.5">
              <Label htmlFor="p-max-tokens" className="text-sm">
                {t("interface.m1410")}
              </Label>
              <p className="text-muted-foreground text-xs">{t("interface.m1411")}</p>
            </div>
            <Input
              id="p-max-tokens"
              disabled={useSubscription}
              type="number"
              min={0}
              className="w-28 shrink-0"
              value={d.maxTokens}
              onChange={(e) => d.setMaxTokens(e.target.value)}
              placeholder="0"
            />
          </div>
        )}
        <div
          className={
            skipLimits
              ? "flex items-center justify-between gap-4"
              : "flex items-center justify-between gap-4 border-t pt-3"
          }
        >
          <div className="grid gap-0.5">
            <Label className="text-sm">{t("interface.m1412")}</Label>
            <p className="text-muted-foreground text-xs">{MAX_TOKENS_FIELD_HINTS[d.format]}</p>
          </div>
          <Select
            value={d.format === "openai" ? d.maxTokensField : NONE}
            onValueChange={d.setMaxTokensField}
            disabled={d.format !== "openai"}
          >
            <SelectTrigger className="w-56 shrink-0">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {MAX_TOKENS_FIELDS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="grid gap-3 rounded-lg border p-3">
        <div className="flex items-center justify-between gap-4">
          <div className="grid gap-0.5">
            <Label className="text-sm">{t("interface.m1413")}</Label>
            <p className="text-muted-foreground text-xs">{t("interface.m1414")}</p>
          </div>
          <Select value={d.thinkingType} onValueChange={d.setThinkingType} disabled={useSubscription}>
            <SelectTrigger className="w-32 shrink-0">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {THINKING_TYPES.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex items-center justify-between gap-4 border-t pt-3">
          <div className="grid gap-0.5">
            <Label className="text-sm">{t("interface.m1415")}</Label>
            <p className="text-muted-foreground text-xs">{t("interface.m1416")}</p>
          </div>
          <Select value={d.effort} onValueChange={d.setEffort}>
            <SelectTrigger className="w-32 shrink-0">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {EFFORT_LEVELS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      {hasPriceState(d) && <ProfilePriceFields d={d} />}

      <ProfileRetryFields value={d.retry} onChange={d.setRetry} />
    </>
  );
}

// Format selector shared by the full form and the guided flow's advanced section.
export function FormatSelect({ d }: { d: ProfileDraft }) {
  const { t } = useI18n();
  return (
    <div className="grid gap-2">
      <Label>{t("interface.m1388")}</Label>
      <Select
        disabled={d.useSubscription}
        value={d.format}
        onValueChange={(v) => d.setFormat(v as "anthropic" | "openai" | "openai-responses")}
      >
        <SelectTrigger>
          <SelectValue placeholder={t("interface.m1389")} />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="anthropic">Anthropic</SelectItem>
          <SelectItem value="openai">OpenAI (Chat Completions)</SelectItem>
          <SelectItem value="openai-responses">OpenAI (Responses API)</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}
