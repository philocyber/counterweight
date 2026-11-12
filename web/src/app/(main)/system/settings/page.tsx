"use client";

import * as React from "react";

import {
  CoinsIcon,
  CpuIcon,
  FlaskConicalIcon,
  KeyboardIcon,
  RadioTowerIcon,
  SearchIcon,
  ShieldAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { ErrorState } from "@/components/error-state";
import { ExpandableText } from "@/components/expandable-text";
import { GitHubTokenCard } from "@/components/github-token-card";
import { JiraIntegrationCard } from "@/components/jira-integration-card";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { ApiTokensCard } from "./_components/api-tokens-card";
import { ExecutionIsolationCard } from "./_components/execution-card";
import { ScanSettingsCard } from "./_components/scan-card";
import { UpdateCard } from "./_components/update-card";

type SettingsTab = "general" | "integrations" | "network" | "capture" | "experimental";

// URL hash to section. Existing links (#jira from the Jira dialog and the get-started card) land on
// Integrations and then scroll to their card.
const TAB_BY_HASH: Record<string, SettingsTab> = {
  general: "general",
  integrations: "integrations",
  jira: "integrations",
  github: "integrations",
  network: "network",
  capture: "capture",
  experimental: "experimental",
};

export default function SystemSettingsPage() {
  "use no memo";
  const { t: swt, rich, locale: swLocale } = useI18n();

  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  // loaded is true only after settings were fetched successfully; every control that depends on a stored
  // value stays disabled otherwise, so defaults are never shown as if they were real.
  const [loaded, setLoaded] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // Operational constraint injection targets; both enabled by default.
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // Experimental noa context compression, off by default.
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // Default token budget for new tasks; null when the server does not offer the setting.
  const [defaultBudget, setDefaultBudget] = React.useState<string | null>(null);
  const [savingBudget, setSavingBudget] = React.useState(false);
  // Browser-only preference stored directly in localStorage, not /api/settings.
  const sendMode = useChatSendMode();
  const [tab, setTab] = React.useState<SettingsTab>("general");

  // Follow the URL hash: select the section it names and scroll to the card it points at.
  React.useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const followHash = () => {
      const id = window.location.hash.slice(1);
      const target = TAB_BY_HASH[id];
      if (!target) return;
      setTab(target);
      if (id !== target) {
        clearTimeout(timer);
        timer = setTimeout(() => document.getElementById(id)?.scrollIntoView({ block: "start" }), 50);
      }
    };
    followHash();
    window.addEventListener("hashchange", followHash);
    return () => {
      window.removeEventListener("hashchange", followHash);
      clearTimeout(timer);
    };
  }, []);

  const selectTab = (value: string) => {
    setTab(value as SettingsTab);
    window.history.replaceState(window.history.state, "", `#${value}`);
  };

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
    setDefaultBudget(s.default_task_token_budget === undefined ? null : String(s.default_task_token_budget));
  }, []);

  const saveDefaultBudget = () => {
    const n = Number(defaultBudget);
    if (defaultBudget === null || defaultBudget.trim() === "" || !Number.isInteger(n) || n < 0) {
      toast.error(swt("budget.defaultInvalid"));
      return;
    }
    setSavingBudget(true);
    api
      .setSettings({ default_task_token_budget: n })
      .then((s) => {
        apply(s);
        toast.success(swt("budget.defaultSaved"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSavingBudget(false));
  };

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error(swt("interface.m1627"));
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success(swt("interface.m1628"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success(swt("interface.m1629"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  const loadSettings = React.useCallback(() => {
    setLoadError(null);
    api
      .settings()
      .then((s) => {
        apply(s);
        setLoaded(true);
      })
      .catch((e: Error) => setLoadError(e.message));
  }, [apply]);

  React.useEffect(() => {
    loadSettings();
  }, [loadSettings]);

  const settingSaveFailed = (name: string, e: Error) =>
    toast.error(swt("states.settingSaveFailed", { p0: name, p1: e.message }));

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch((e) => {
        setTrafficCapture(!v); // revert on failure
        settingSaveFailed(swt("interface.m0209"), e);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch((e) => {
        setInjectPlanner(!v); // revert on failure
        settingSaveFailed(swt("interface.m1685"), e);
      });
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? swt("interface.m1630") : swt("interface.m1631"));
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        settingSaveFailed(swt("interface.m1654"), e);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch((e) => {
        setInjectWorker(!v); // revert on failure
        settingSaveFailed(swt("interface.m1688"), e);
      });
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? swt("interface.m1632") : swt("interface.m1633"));
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        settingSaveFailed(swt("interface.m1691"), e);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success(swt("interface.m1634"));
      })
      .catch((e) => {
        toast.error(swt("interface.m1450") + (e as Error).message);
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success(swt("interface.m1635"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success(swt("interface.m1636"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? swt("interface.m1637") : swt("interface.m1638"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(globalProxyInput.trim() ? swt("interface.m1639") : swt("interface.m1640"));
      })
      .catch((e) => toast.error(swt("interface.m1450") + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(swt("interface.m1641", { p0: r.backend, p1: r.count }));
        else toast.error(swt("interface.m1642") + (r.error || swt("interface.m1643")));
      })
      .catch((e) => toast.error(swt("interface.m1642") + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <PageHeader title={swt("interface.m1644")} description={swt("interface.m1645")} />

      {loadError !== null && (
        <Card className="py-0">
          <ErrorState title={swt("states.settingsLoadFailed")} detail={loadError} onRetry={loadSettings} />
        </Card>
      )}

      <ExecutionIsolationCard />

      {/* Sections are plain grids so the visual order is the DOM order. Every panel stays mounted (hidden when inactive) so unsaved input in a card survives switching sections. */}
      <Tabs value={tab} onValueChange={selectTab}>
        <TabsList aria-label={swt("nav.settingsTabsLabel")} className="h-auto flex-wrap justify-start">
          <TabsTrigger value="general">{swt("nav.settingsTabGeneral")}</TabsTrigger>
          <TabsTrigger value="integrations">{swt("nav.settingsTabIntegrations")}</TabsTrigger>
          <TabsTrigger value="network">{swt("nav.settingsTabNetwork")}</TabsTrigger>
          <TabsTrigger value="capture">{swt("nav.settingsTabCapture")}</TabsTrigger>
          <TabsTrigger value="experimental">{swt("nav.settingsTabExperimental")}</TabsTrigger>
        </TabsList>

        <TabsContent value="general" forceMount className="flex flex-col gap-4 data-[state=inactive]:hidden md:gap-6">
          <p className="text-muted-foreground text-sm">{swt("nav.settingsGeneralHint")}</p>
          <div className="grid items-start gap-4 md:gap-6 lg:grid-cols-2">
            <div className="[&>[data-slot=card]]:mb-0">
              <UpdateCard />
            </div>

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <CpuIcon className="size-4" />
                  {swt("interface.m1751")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {rich("settings.workers", {}, { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> })}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                <div className="flex items-center gap-2">
                  <Input
                    type="number"
                    min={1}
                    className="w-32 font-mono text-sm"
                    placeholder="3"
                    value={workers}
                    disabled={!loaded || savingWorkers}
                    onChange={(e) => setWorkers(e.target.value)}
                  />
                  <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                    {swt("interface.m0273")}
                  </Button>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  {swt("interface.m1747")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {rich("settings.python", {}, { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> })}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                <div className="flex items-center gap-2">
                  <Input
                    className="font-mono text-sm"
                    placeholder={swt("interface.m1749")}
                    value={pyInterp}
                    disabled={!loaded || saving}
                    onChange={(e) => setPyInterp(e.target.value)}
                  />
                  <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                    {swt("interface.m1750")}
                  </Button>
                  <Button onClick={savePython} disabled={!loaded || saving}>
                    {swt("interface.m0273")}
                  </Button>
                </div>
              </CardContent>
            </Card>

            {defaultBudget !== null && (
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2 text-base">
                    <CoinsIcon className="size-4" />
                    {swt("budget.defaultCardTitle")}
                  </CardTitle>
                  <CardDescription>{swt("budget.defaultCardDescription")}</CardDescription>
                </CardHeader>
                <CardContent className="flex flex-col gap-2">
                  <Label htmlFor="default-token-budget" className="font-normal text-muted-foreground text-sm">
                    {swt("budget.defaultLabel")}
                  </Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="default-token-budget"
                      type="number"
                      min={0}
                      className="w-48 font-mono text-sm"
                      placeholder="0"
                      value={defaultBudget}
                      disabled={!loaded || savingBudget}
                      onChange={(e) => setDefaultBudget(e.target.value)}
                    />
                    <Button onClick={saveDefaultBudget} disabled={!loaded || savingBudget}>
                      {swt("interface.m0273")}
                    </Button>
                  </div>
                </CardContent>
              </Card>
            )}

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <KeyboardIcon className="size-4" />
                  {swt("interface.m1755")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {swt("interface.m1756")}
                    <br />
                    {rich(
                      "settings.sendPreference",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
                  {swt("interface.m1760")}
                </Label>
                <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
                  <SelectTrigger id="chat-send-mode" className="w-72">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {CHAT_SEND_MODE_OPTIONS.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        <TabsContent
          value="integrations"
          forceMount
          className="flex flex-col gap-4 data-[state=inactive]:hidden md:gap-6"
        >
          <p className="text-muted-foreground text-sm">{swt("nav.settingsIntegrationsHint")}</p>
          <div className="grid items-start gap-4 md:gap-6 lg:grid-cols-2">
            <div id="github" className="scroll-mt-20">
              <GitHubTokenCard />
            </div>

            <JiraIntegrationCard />

            <ScanSettingsCard />

            <div id="api-tokens" className="scroll-mt-20 lg:col-span-2">
              <ApiTokensCard />
            </div>
          </div>
        </TabsContent>

        <TabsContent value="network" forceMount className="flex flex-col gap-4 data-[state=inactive]:hidden md:gap-6">
          <p className="text-muted-foreground text-sm">{swt("nav.settingsNetworkHint")}</p>
          <div className="grid items-start gap-4 md:gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  {swt("interface.m1660")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {rich("settings.proxy", {}, { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> })}
                    <br />
                    {rich(
                      "settings.proxyCapture",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                    <br />
                    {rich(
                      "settings.proxySocks",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-2">
                <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
                  {swt("interface.m1673")}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="global-proxy"
                    autoComplete="off"
                    placeholder={swt("interface.m1674")}
                    value={globalProxyInput}
                    disabled={!loaded || savingGlobalProxy}
                    onChange={(e) => setGlobalProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                    {swt("interface.m0273")}
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">
                  {globalProxyInput.trim() ? swt("interface.m1675") : swt("interface.m1676")}
                </p>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <SearchIcon className="size-4" />
                  {swt("interface.m1187")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {rich(
                      "settings.webSearch",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                    <br />
                    {rich(
                      "settings.searchSources",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-4">
                <div className="flex items-center justify-between gap-4">
                  <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                    {webSearch ? swt("interface.m1709") : swt("interface.m1710")}
                  </Label>
                  <Switch
                    id="web-search"
                    checked={webSearch}
                    disabled={!loaded || saving}
                    onCheckedChange={(v) => {
                      setWebSearch(v); // optimistic
                      saveWebSearch({ web_search_enabled: v });
                    }}
                  />
                </div>

                <p className="text-muted-foreground text-xs">{swt("nav.settingsWebSearchNote")}</p>

                {webSearch && (
                  <div className="flex items-center justify-between gap-4">
                    <Label className="text-sm font-normal text-muted-foreground">{swt("interface.m1711")}</Label>
                    <Select
                      value={backend}
                      disabled={!loaded || saving}
                      onValueChange={(v) => {
                        setBackend(v); // optimistic
                        saveWebSearch({ web_search_backend: v });
                      }}
                    >
                      <SelectTrigger className="w-72 shrink-0">
                        <SelectValue placeholder={swt("interface.m1712")} />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ddgs">{swt("interface.m1713")}</SelectItem>
                        <SelectItem value="brave-free">{swt("interface.m1714")}</SelectItem>
                        <SelectItem value="tavily">{swt("interface.m1715")}</SelectItem>
                        <SelectItem value="deepseek">{swt("interface.m1716")}</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                )}

                {webSearch && backend === "deepseek" && (
                  <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                    <p className="text-sm font-medium">{swt("interface.m1717")}</p>
                    <p className="text-muted-foreground text-xs leading-relaxed">
                      {rich(
                        "settings.deepseekRequirements",
                        {},
                        { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                      )}
                    </p>
                    <p className="text-muted-foreground text-xs leading-relaxed">
                      {rich(
                        "settings.deepseekBehavior",
                        {},
                        { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                      )}
                    </p>
                    <p className="text-muted-foreground text-xs leading-relaxed">{swt("interface.m1734")}</p>
                  </div>
                )}

                {webSearch && backend === "brave-free" && (
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                      {swt("english.e130")}{" "}
                      {braveKeySet && (
                        <span className="ml-2 text-xs text-emerald-700 dark:text-emerald-400">
                          {swt("interface.m0207")}
                        </span>
                      )}
                    </Label>
                    <div className="flex items-center gap-2">
                      <Input
                        id="brave-key"
                        type="password"
                        autoComplete="off"
                        placeholder={braveKeySet ? swt("interface.m1735") : swt("interface.m1736")}
                        value={braveKeyInput}
                        disabled={!loaded || savingKey}
                        onChange={(e) => setBraveKeyInput(e.target.value)}
                      />
                      <Button
                        type="button"
                        onClick={saveBraveKey}
                        disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                      >
                        {swt("interface.m0273")}
                      </Button>
                    </div>
                    {braveNeedsKey && (
                      <p className="text-xs text-amber-700 dark:text-amber-400">{swt("interface.m1737")}</p>
                    )}
                    <p className="text-muted-foreground text-xs">{swt("interface.m1738")}</p>
                  </div>
                )}

                {webSearch && backend === "tavily" && (
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                      {swt("english.e131")}{" "}
                      {tavilyKeySet && (
                        <span className="ml-2 text-xs text-emerald-700 dark:text-emerald-400">
                          {swt("interface.m0207")}
                        </span>
                      )}
                    </Label>
                    <div className="flex items-center gap-2">
                      <Input
                        id="tavily-key"
                        type="password"
                        autoComplete="off"
                        placeholder={tavilyKeySet ? swt("interface.m1735") : swt("interface.m1739")}
                        value={tavilyKeyInput}
                        disabled={!loaded || savingTavilyKey}
                        onChange={(e) => setTavilyKeyInput(e.target.value)}
                      />
                      <Button
                        type="button"
                        onClick={saveTavilyKey}
                        disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                      >
                        {swt("interface.m0273")}
                      </Button>
                    </div>
                    {webSearch && backend === "tavily" && !tavilyKeySet && (
                      <p className="text-xs text-amber-700 dark:text-amber-400">{swt("interface.m1740")}</p>
                    )}
                    <p className="text-muted-foreground text-xs">{swt("interface.m1741")}</p>
                  </div>
                )}

                {webSearch && (
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                      {swt("interface.m1742")}
                    </Label>
                    <div className="flex items-center gap-2">
                      <Input
                        id="ws-proxy"
                        autoComplete="off"
                        placeholder={swt("interface.m1743")}
                        value={proxyInput}
                        disabled={!loaded || savingProxy}
                        onChange={(e) => setProxyInput(e.target.value)}
                      />
                      <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                        {swt("interface.m0273")}
                      </Button>
                    </div>
                    <p className="text-muted-foreground text-xs">{swt("interface.m1744")}</p>
                  </div>
                )}

                {webSearch && (
                  <div className="flex items-center justify-between gap-4 border-t pt-4">
                    <p className="text-muted-foreground text-xs">{swt("interface.m1745")}</p>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={runTest}
                      disabled={!loaded || testing}
                      className="shrink-0"
                    >
                      {testing ? swt("interface.m1417") : swt("interface.m1746")}
                    </Button>
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        <TabsContent value="capture" forceMount className="flex flex-col gap-4 data-[state=inactive]:hidden md:gap-6">
          <p className="text-muted-foreground text-sm">{swt("nav.settingsCaptureHint")}</p>
          <div className="grid items-start gap-4 md:gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  {swt("interface.m0209")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {swt("interface.m1646")}
                    <br />
                    {rich(
                      "settings.captureDisabled",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
                  {trafficCapture ? swt("interface.m1652") : swt("interface.m1653")}
                </Label>
                <Switch
                  id="traffic-capture"
                  checked={trafficCapture}
                  disabled={!loaded || saving}
                  onCheckedChange={toggleTraffic}
                />
              </CardContent>
              <CardContent className="pt-0">
                <p className="text-xs text-amber-700 dark:text-amber-400">{swt("nav.settingsCaptureNote")}</p>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <RadioTowerIcon className="size-4" />
                  {swt("interface.m1654")}
                </CardTitle>
                <CardDescription id="agent-traffic-binding-description">
                  <ExpandableText>
                    {rich("settings.binding", {}, { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> })}
                    <br />
                    {swt("interface.m1657")}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
                  {agentTrafficBinding ? swt("interface.m1658") : swt("interface.m1659")}
                </Label>
                <Switch
                  id="agent-traffic-binding"
                  aria-describedby="agent-traffic-binding-description"
                  checked={agentTrafficBinding}
                  disabled={!loaded || saving}
                  onCheckedChange={toggleAgentTrafficBinding}
                />
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <ShieldAlertIcon className="size-4" />
                  {swt("interface.m1677")}
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {rich(
                      "settings.constraints",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                    <br />
                    {rich(
                      "settings.constraintTargets",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-4">
                <div className="flex items-center justify-between gap-4">
                  <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                    {swt("interface.m1685")} {injectPlanner ? swt("interface.m1686") : swt("interface.m1687")}
                  </Label>
                  <Switch
                    id="inject-planner"
                    checked={injectPlanner}
                    disabled={!loaded}
                    onCheckedChange={toggleInjectPlanner}
                  />
                </div>
                <div className="flex items-center justify-between gap-4">
                  <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                    {swt("interface.m1688")} {injectWorker ? swt("interface.m1686") : swt("interface.m1687")}
                  </Label>
                  <Switch
                    id="inject-worker"
                    checked={injectWorker}
                    disabled={!loaded}
                    onCheckedChange={toggleInjectWorker}
                  />
                </div>
              </CardContent>
              <CardContent className="pt-0">
                <p className="text-xs text-amber-700 dark:text-amber-400">{swt("nav.settingsConstraintsNote")}</p>
              </CardContent>
            </Card>
          </div>
        </TabsContent>

        <TabsContent
          value="experimental"
          forceMount
          className="flex flex-col gap-4 data-[state=inactive]:hidden md:gap-6"
        >
          <p className="text-muted-foreground text-sm">{swt("nav.settingsExperimentalHint")}</p>
          <div className="grid items-start gap-4 md:gap-6 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <FlaskConicalIcon className="size-4" />
                  {swt("interface.m1689")}
                  <Badge variant="outline">{swt("nav.settingsExperimentalBadge")}</Badge>
                </CardTitle>
                <CardDescription>
                  <ExpandableText>
                    {swt("interface.m1690")}
                    <br />
                    {rich(
                      "settings.compression",
                      {},
                      { b: (text) => <b>{text}</b>, code: (text) => <code>{text}</code> },
                    )}
                  </ExpandableText>
                </CardDescription>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-4">
                <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
                  {swt("interface.m1691")} {noaCompaction ? swt("interface.m1686") : swt("interface.m1687")}
                </Label>
                <Switch
                  id="noa-compaction"
                  checked={noaCompaction}
                  disabled={!loaded}
                  onCheckedChange={toggleNoaCompaction}
                />
              </CardContent>
              <CardContent className="pt-0">
                <p className="text-xs text-muted-foreground">{swt("nav.settingsCompactionNote")}</p>
              </CardContent>
            </Card>
          </div>
        </TabsContent>
      </Tabs>
    </div>
  );
}
