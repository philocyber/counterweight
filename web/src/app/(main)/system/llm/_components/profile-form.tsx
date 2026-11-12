"use client";

import { Loader2Icon, PlugZapIcon, PlusIcon, RefreshCwIcon, SaveIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useI18n } from "@/i18n";
import type { ChatGPTModel } from "@/lib/types";

import type { ProfileDraft } from "./profile-draft";
import { EndpointWarnings, FormatSelect, ModelPicker, ProfileAdvancedFields } from "./profile-fields";
import { SubscriptionConnection } from "./subscription";

const daybreakProgram = (choice?: ChatGPTModel) =>
  choice?.cyber_programs?.find((program) => program === "daybreakBlue" || program === "daybreakRed");

// The complete profile form: editing an existing profile, and the ChatGPT / Codex sign-in for a new one.
export function ProfileFullForm({ d }: { d: ProfileDraft }) {
  const { t } = useI18n();
  const { isNew, useSubscription } = d;

  return (
    <>
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
        {isNew ? (
          <Button type="button" variant="link" className="h-auto w-fit p-0 text-xs" onClick={d.backToGuided}>
            {t("models.chooseAnotherWay")}
          </Button>
        ) : (
          <div className="grid gap-2">
            <Label>{t("providers.authMethod")}</Label>
            <Select
              value={useSubscription ? "subscription" : "api"}
              onValueChange={(v) => d.changeAuthMethod(v as "api" | "subscription")}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="api">{t("providers.apiKeyMethod")}</SelectItem>
                <SelectItem value="subscription">{t("providers.subscriptionMethod")}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        )}
        {d.open && useSubscription && (
          <SubscriptionConnection
            value={d.subscriptionId}
            onChange={(id) => {
              d.setSubscriptionId(id);
              d.bumpSubscriptionModels();
              d.setCyberProgram("standard");
              d.setModels([]);
              d.setSubscriptionModels([]);
              d.setModel("");
            }}
          />
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label htmlFor="p-name">{t("interface.m0868")}</Label>
            <Input
              id="p-name"
              placeholder={t("interface.m1387")}
              value={d.name}
              onChange={(e) => d.setName(e.target.value)}
            />
          </div>
          <FormatSelect d={d} />
        </div>

        <div className="grid gap-2">
          <Label htmlFor="p-model">{t("interface.m0627")}</Label>
          {useSubscription ? (
            <>
              <div className="flex gap-2">
                <Select
                  value={d.model}
                  onValueChange={(slug) => {
                    d.setModel(slug);
                    if (d.cyberProgram !== "standard")
                      d.setCyberProgram(
                        daybreakProgram(d.subscriptionModels.find((choice) => choice.slug === slug)) || "standard",
                      );
                  }}
                  disabled={!d.subscriptionId || d.loadingModels || !d.subscriptionModels.length}
                >
                  <SelectTrigger id="p-model" className="w-full">
                    <SelectValue placeholder={t("providers.subscriptionModelPlaceholder")}>
                      {d.subscriptionModels.find((choice) => choice.slug === d.model)?.display_name}
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent className="max-h-72">
                    {d.subscriptionModels.map((choice) => (
                      <SelectItem
                        key={choice.slug}
                        value={choice.slug}
                        disabled={d.cyberProgram !== "standard" && !daybreakProgram(choice)}
                      >
                        <span className="flex flex-col items-start">
                          <span>{choice.display_name}</span>
                          <span className="font-mono text-muted-foreground text-xs">{choice.slug}</span>
                        </span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="shrink-0"
                  disabled={!d.subscriptionId || d.loadingModels}
                  onClick={d.bumpSubscriptionModels}
                  title={t("interface.m1390")}
                >
                  {d.loadingModels ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
                </Button>
              </div>
              {d.loadingModels && (
                <p className="text-muted-foreground text-xs">{t("providers.subscriptionModelsLoading")}</p>
              )}
              <p className="text-muted-foreground text-xs">{t("providers.subscriptionModelsHelp")}</p>
              <div className="flex items-center justify-between rounded-md border px-3 py-2">
                <div>
                  <Label htmlFor="p-daybreak">Daybreak</Label>
                  <p className="text-muted-foreground text-xs">{t("providers.subscriptionDaybreakHelp")}</p>
                </div>
                <Switch
                  id="p-daybreak"
                  checked={d.cyberProgram !== "standard"}
                  disabled={
                    d.loadingModels ||
                    (d.cyberProgram === "standard" && !d.subscriptionModels.some((choice) => daybreakProgram(choice)))
                  }
                  onCheckedChange={(enabled) => {
                    if (!enabled) {
                      d.setCyberProgram("standard");
                      return;
                    }
                    const choice =
                      d.subscriptionModels.find((choice) => choice.slug === d.model && daybreakProgram(choice)) ||
                      d.subscriptionModels.find((choice) => daybreakProgram(choice));
                    if (choice) {
                      d.setModel(choice.slug);
                      d.setCyberProgram(daybreakProgram(choice)!);
                    }
                  }}
                  aria-label="Daybreak"
                />
              </div>
            </>
          ) : (
            <ModelPicker d={d} />
          )}
        </div>

        {!useSubscription && (
          <>
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

            <div className="grid gap-2">
              <Label htmlFor="p-api-key">{t("english.e100")}</Label>
              <Input
                id="p-api-key"
                type="password"
                placeholder={d.keyHint ? t("interface.m1399", { p0: d.keyHint }) : "sk-…"}
                value={d.apiKey}
                onChange={(e) => d.setApiKey(e.target.value)}
              />
            </div>
          </>
        )}

        <ProfileAdvancedFields d={d} />
      </div>

      <div className="flex gap-2 border-t px-4 py-3">
        <Button variant="outline" onClick={d.testConnection} disabled={d.testing}>
          {d.testing ? <Loader2Icon className="animate-spin" /> : <PlugZapIcon />}
          {d.testing ? t("interface.m1417") : t("interface.m1418")}
        </Button>
        <Button onClick={() => void d.save()} disabled={d.saving} className="flex-1">
          {d.saving && <Loader2Icon className="animate-spin" />}
          {!d.saving && (isNew ? <PlusIcon /> : <SaveIcon />)}
          {isNew ? t("interface.m0728") : t("interface.m0273")}
        </Button>
      </div>
    </>
  );
}
