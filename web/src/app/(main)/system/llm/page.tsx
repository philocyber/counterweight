"use client";

import * as React from "react";

import { PencilIcon, PlugZapIcon, PlusIcon, RotateCcwIcon, StarIcon, Trash2Icon, ZapIcon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { ErrorState } from "@/components/error-state";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { LLMPoolMember, LLMPoolStatus, LLMProfile } from "@/lib/types";
import { cn } from "@/lib/utils";

import { ProfileSheet } from "./_components/profile-sheet";
import { RetryPolicyPanel } from "./_components/retry";

function cooldownText(secs: number) {
  if (secs <= 0) return "";
  if (secs < 60) return `${secs}s`;
  return `${Math.ceil(secs / 60)}min`;
}

// Profile health prioritizes missing API keys because requests cannot be sent at all;
// other states come from rotation circuit records, which exist only while the profile is monitored.
type Health = { label: string; cls: string; hint?: string };
function healthOf(p: LLMProfile, m: LLMPoolMember | undefined, monitored: boolean): Health {
  if (!p.api_key_hint && !p.subscription_id) {
    return {
      label: swt("interface.m1334"),
      cls: "border-muted-foreground/40 text-muted-foreground",
      hint: swt("interface.m1335"),
    };
  }
  if (m?.state === "tripped") {
    return {
      label:
        m.cooldown_secs > 0 ? swt("interface.m1336", { p0: cooldownText(m.cooldown_secs) }) : swt("interface.m1337"),
      cls: "border-destructive/50 text-destructive",
      hint: m.last_error,
    };
  }
  if (m?.state === "degraded") {
    return {
      label: swt("interface.m1338", { p0: m.fails }),
      cls: "border-amber-500/50 text-amber-700 dark:text-amber-400",
      hint: m.last_error,
    };
  }
  // Failures are recorded only for profiles in an active rotation. Anything else has no record either way,
  // so it is not called Healthy.
  if (!m || !monitored) {
    return {
      label: swt("models.healthUnmonitored"),
      cls: "border-muted-foreground/40 text-muted-foreground",
      hint: swt("models.healthUnmonitoredHint"),
    };
  }
  return { label: swt("interface.m1339"), cls: "border-emerald-500/50 text-emerald-700 dark:text-emerald-400" };
}

// ─────────────────────────────────────────────────────────────────────────────
// Rotation configuration drawer.
// ─────────────────────────────────────────────────────────────────────────────

function PoolSheet({
  open,
  onOpenChange,
  pool,
  onReload,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  pool: LLMPoolStatus | null;
  onReload: () => Promise<void>;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [busy, setBusy] = React.useState(false);

  // Backend computes cooldown seconds. Poll only with the drawer open and unhealthy profiles present.
  React.useEffect(() => {
    if (!open || !pool?.enabled || !pool.chain.some((m) => m.state !== "ok")) return;
    const t = setInterval(() => void onReload(), 10_000);
    return () => clearInterval(t);
  }, [open, pool, onReload]);

  async function toggle(patch: { llm_pool_enabled?: boolean; llm_pool_bind_fallback?: boolean }) {
    if (busy) return;
    setBusy(true);
    try {
      await api.setSettings(patch);
      await onReload();
      if (patch.llm_pool_enabled !== undefined) {
        toast.success(patch.llm_pool_enabled ? swt("interface.m1340") : swt("interface.m1341"));
      } else {
        toast.success(swt("interface.m1342"));
      }
    } catch (e) {
      toast.error(swt("interface.m1343", { p0: (e as Error).message }));
    } finally {
      setBusy(false);
    }
  }

  async function recover(id?: string) {
    try {
      await api.resetLLMPool(id);
      await onReload();
      toast.success(id ? swt("interface.m1344") : swt("interface.m1345"));
    } catch (e) {
      toast.error(swt("interface.m1346", { p0: (e as Error).message }));
    }
  }

  const enabled = pool?.enabled ?? false;
  const chain = pool?.chain ?? [];
  // Rotation participants exclude opted-out profiles and retain the backend's actual attempt order.
  const inChain = chain.filter((m) => m.active || !m.excluded);
  const tripped = chain.filter((m) => m.state === "tripped");

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex flex-col gap-0 p-0 data-[side=right]:sm:max-w-lg">
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            <ZapIcon className="size-4" /> {swt("interface.m1347")}
          </SheetTitle>
          <SheetDescription>
            {swt("interface.m1348")}
            <b>{swt("interface.m1349")}</b>
            {swt("interface.m1350")}
          </SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-6">
          <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
            <div className="grid gap-0.5">
              <Label className="text-sm">{swt("interface.m1351")}</Label>
              <p className="text-muted-foreground text-xs">{swt("interface.m1352")}</p>
            </div>
            <Switch
              checked={enabled}
              disabled={busy}
              onCheckedChange={(v) => void toggle({ llm_pool_enabled: v })}
              aria-label={swt("interface.m1353")}
            />
          </div>

          {enabled && (
            <>
              <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
                <div className="grid gap-0.5">
                  <Label className="text-sm">{swt("interface.m1354")}</Label>
                  <p className="text-muted-foreground text-xs">{swt("interface.m1355")}</p>
                </div>
                <Switch
                  checked={pool?.bind_fallback ?? false}
                  disabled={busy}
                  onCheckedChange={(v) => void toggle({ llm_pool_bind_fallback: v })}
                  aria-label={swt("interface.m1356")}
                />
              </div>

              <Separator />

              <div className="grid gap-2">
                <div className="flex items-center justify-between">
                  <Label className="text-sm">{swt("interface.m1357")}</Label>
                  {tripped.length > 0 && (
                    <Button size="sm" variant="ghost" onClick={() => void recover()}>
                      <RotateCcwIcon /> {swt("interface.m1358")}
                    </Button>
                  )}
                </div>
                {inChain.length < 2 && (
                  <p className="text-muted-foreground text-xs">
                    {swt("interface.m1359")} {inChain.length} {swt("interface.m1360")}
                  </p>
                )}
                {chain.map((m) => {
                  const excluded = m.excluded && !m.active;
                  const order = excluded ? null : inChain.findIndex((x) => x.profile_id === m.profile_id) + 1;
                  return (
                    <div
                      key={m.profile_id}
                      className={cn(
                        "grid gap-1 rounded-lg border p-2.5 text-sm",
                        excluded && "opacity-55",
                        m.state === "tripped" && "border-destructive/40",
                      )}
                    >
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="w-5 shrink-0 text-center font-mono text-muted-foreground text-xs">
                          {order ?? "—"}
                        </span>
                        <span className="font-medium">{m.name}</span>
                        {m.active && (
                          <Badge variant="outline" className="border-amber-400/50 text-amber-700 dark:text-amber-400">
                            {swt("interface.m1361")}
                          </Badge>
                        )}
                        {excluded && <Badge variant="outline">{swt("interface.m1362")}</Badge>}
                        <div className="ml-auto flex items-center gap-2">
                          {m.state === "tripped" && m.cooldown_secs > 0 && (
                            <span className="text-muted-foreground text-xs">
                              {swt("interface.m1363")} {cooldownText(m.cooldown_secs)}
                            </span>
                          )}
                          {m.state === "degraded" && (
                            <span className="text-muted-foreground text-xs">
                              {swt("interface.m1364")} {m.fails} {swt("interface.m1365")}
                            </span>
                          )}
                          {m.state !== "ok" && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-7"
                              aria-label={swt("interface.m1366")}
                              title={swt("interface.m1367")}
                              onClick={() => void recover(m.profile_id)}
                            >
                              <RotateCcwIcon className="size-3.5" />
                            </Button>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-x-3 pl-7 text-muted-foreground text-xs">
                        <code className="truncate font-mono">{m.model}</code>
                        {!m.active && (
                          <span>
                            {swt("interface.m1243")} {m.priority}
                          </span>
                        )}
                      </div>
                      {m.last_error && (
                        <p className="truncate pl-7 font-mono text-muted-foreground text-xs" title={m.last_error}>
                          {m.last_error}
                        </p>
                      )}
                    </div>
                  );
                })}
                {chain.length === 0 && (
                  <div className="rounded-lg border border-dashed p-4 text-center text-muted-foreground text-sm">
                    {swt("interface.m1368")}
                  </div>
                )}
              </div>

              <div className="rounded-lg border border-dashed p-3 text-muted-foreground text-xs leading-relaxed">
                {swt("interface.m1369")}
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────

export default function LLMPage() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [confirm, confirmDialog] = useConfirm();

  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  // Whether the profile list was ever fetched, and the message of the latest failed fetch (null when healthy).
  const [profilesLoaded, setProfilesLoaded] = React.useState(false);
  const [profilesError, setProfilesError] = React.useState<string | null>(null);
  const [pool, setPool] = React.useState<LLMPoolStatus | null>(null);
  const [poolOpen, setPoolOpen] = React.useState(false);
  // Store drawer visibility separately from editing data to avoid the closing animation changing
  // the title from Edit X to New. editing=null means creation.
  const [initialSubscription, setInitialSubscription] = React.useState(false);
  const [editOpen, setEditOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<LLMProfile | null>(null);
  const openEditor = React.useCallback((p: LLMProfile | null, subscription = false) => {
    setInitialSubscription(subscription);
    setEditing(p);
    setEditOpen(true);
  }, []);

  const loadPool = React.useCallback(async () => {
    try {
      setPool(await api.llmPool());
    } catch {
      /* ignore */
    }
  }, []);

  const load = React.useCallback(async () => {
    try {
      setProfiles(await api.llmProfiles());
      setProfilesLoaded(true);
      setProfilesError(null);
    } catch (e) {
      setProfilesError((e as Error).message);
    }
    await loadPool();
  }, [loadPool]);

  React.useEffect(() => {
    void load();
  }, [load]);

  // Profile health badges look up rotation state by profile ID.
  const health = React.useMemo(() => {
    const m = new Map<string, LLMPoolMember>();
    for (const c of pool?.chain ?? []) m.set(c.profile_id, c);
    return m;
  }, [swLocale, pool]);
  // The chain lists every profile, but failures are recorded only when rotation is on and has at least two
  // members to fail over between; with fewer the profile is called directly and nothing is tracked.
  const rotationMembers = (pool?.chain ?? []).filter((c) => !c.excluded).length;
  const monitored = (m?: LLMPoolMember) => !!pool?.enabled && rotationMembers >= 2 && !!m && !m.excluded;

  async function activate(id: string, name: string) {
    try {
      await api.activateLLMProfile(id);
      toast.success(swt("interface.m1419", { p0: name }));
      await load();
    } catch (e) {
      toast.error(swt("interface.m1420", { p0: (e as Error).message }));
    }
  }

  async function remove(p: LLMProfile) {
    if (p.is_default) {
      toast.error(swt("interface.m1421"));
      return;
    }
    const ok = await confirm({
      title: swt("confirm.deleteProfileTitle", { p0: p.name }),
      description: swt("confirm.deleteProfileDescription"),
      confirmLabel: swt("confirm.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteLLMProfile(p.id);
      toast.success(swt("interface.m1422", { p0: p.name }));
      await load();
    } catch (e) {
      toast.error(swt("interface.m0379", { p0: (e as Error).message }));
    }
  }

  const poolOn = pool?.enabled ?? false;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      {confirmDialog}
      <PageHeader
        title={swt("nav.llm")}
        description={swt("interface.m1423")}
        actions={
          <>
            <Button size="sm" variant="outline" onClick={() => setPoolOpen(true)}>
              <ZapIcon /> {swt("interface.m1424")}{" "}
              {poolOn && (
                <Badge variant="outline" className="ml-1 border-emerald-500/50 text-emerald-700 dark:text-emerald-400">
                  {swt("interface.m1425")}
                </Badge>
              )}
            </Button>
            <Button size="sm" variant="outline" onClick={() => openEditor(null, true)}>
              <PlugZapIcon /> {swt("providers.connectCodex")}
            </Button>
            <Button size="sm" onClick={() => openEditor(null)}>
              <PlusIcon /> {swt("models.connectCta")}
            </Button>
          </>
        }
      />

      <Tabs defaultValue="profiles" className="flex-1">
        <TabsList>
          <TabsTrigger value="profiles">{swt("interface.m1240")}</TabsTrigger>
          <TabsTrigger value="retry">{swt("interface.m1426")}</TabsTrigger>
        </TabsList>

        <TabsContent value="profiles" className="mt-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {profiles.map((p) => {
              const h = healthOf(p, health.get(p.id), monitored(health.get(p.id)));
              return (
                // Mouse users can click anywhere on the card; keyboard users get the Edit button below, so
                // the card itself is not an interactive role and does not nest controls.
                <Card
                  key={p.id}
                  onClick={() => openEditor(p)}
                  className={cn(
                    "cursor-pointer gap-0 py-4 outline-none transition-colors hover:border-foreground/30",
                    p.is_default && "border-amber-400/50 bg-amber-400/5",
                  )}
                >
                  <CardContent className="grid gap-2 px-4">
                    <div className="flex items-start gap-2">
                      <StarIcon
                        className={cn(
                          "mt-0.5 size-4 shrink-0",
                          p.is_default ? "fill-amber-400 text-amber-700 dark:text-amber-400" : "text-muted-foreground",
                        )}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="truncate font-medium text-sm">{p.name}</span>
                          <Badge variant="outline" className="uppercase">
                            {p.format}
                          </Badge>
                          {p.is_default && (
                            <Badge variant="outline" className="border-amber-400/50 text-amber-700 dark:text-amber-400">
                              {swt("models.activeBadge")}
                            </Badge>
                          )}
                          <Badge variant="outline" className={cn("ml-auto", h.cls)} title={h.hint}>
                            {h.label}
                          </Badge>
                        </div>
                        <code className="mt-1 block truncate font-mono text-muted-foreground text-xs">{p.model}</code>
                      </div>
                    </div>

                    <div className="flex flex-wrap gap-x-3 gap-y-0.5 pl-6 text-muted-foreground text-xs">
                      {p.subscription_id ? (
                        <span>{swt("providers.subscriptionMethod")}</span>
                      ) : (
                        p.api_key_hint && <span>{p.api_key_hint}</span>
                      )}
                      <span>
                        {p.rate_per_second}
                        {swt("english.e102")}
                        {p.rate_per_minute}
                        {swt("english.e103")}
                      </span>
                      {p.proxy && (
                        <span className="truncate">
                          {swt("interface.m1427")} {p.proxy}
                        </span>
                      )}
                      {p.reasoning_effort && (
                        <span>
                          {swt("interface.m1428")}{" "}
                          {p.reasoning_effort === "off" ? swt("interface.m1429") : p.reasoning_effort}
                        </span>
                      )}
                      {/* Show rotation fields only while rotation is enabled. */}
                      {poolOn &&
                        !p.is_default &&
                        (p.pool_exclude ? (
                          <span>{swt("interface.m1362")}</span>
                        ) : (
                          <span>
                            {swt("interface.m1243")} {p.priority ?? 0}
                          </span>
                        ))}
                    </div>

                    <div className="mt-1 flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        className="flex-1"
                        disabled={p.is_default}
                        onClick={(e) => {
                          e.stopPropagation();
                          void activate(p.id, p.name);
                        }}
                      >
                        {p.is_default ? swt("interface.m1430") : swt("interface.m1431")}
                      </Button>
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label={swt("english.e216")}
                        onClick={(e) => {
                          e.stopPropagation();
                          openEditor(p);
                        }}
                      >
                        <PencilIcon />
                      </Button>
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label={swt("interface.m1432")}
                        onClick={(e) => {
                          e.stopPropagation();
                          void remove(p);
                        }}
                      >
                        <Trash2Icon className="text-destructive" />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
            {profilesError !== null && !profilesLoaded ? (
              <div className="col-span-full rounded-lg border border-dashed">
                <ErrorState
                  title={swt("states.profilesLoadFailed")}
                  detail={profilesError}
                  onRetry={() => void load()}
                />
              </div>
            ) : profiles.length === 0 ? (
              profilesLoaded && (
                <div className="col-span-full grid justify-items-center gap-3 rounded-lg border border-dashed p-10 text-center text-muted-foreground text-sm">
                  {swt("models.emptyProfiles")}
                  <Button size="sm" onClick={() => openEditor(null)}>
                    <PlusIcon /> {swt("models.connectCta")}
                  </Button>
                </div>
              )
            ) : (
              <button
                type="button"
                onClick={() => openEditor(null)}
                className="flex min-h-36 flex-col items-center justify-center gap-2 rounded-lg border border-dashed text-muted-foreground text-sm outline-none transition-colors hover:border-foreground/30 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              >
                <PlusIcon className="size-5" />
                {swt("english.e213")}
              </button>
            )}
          </div>
        </TabsContent>

        <TabsContent value="retry" className="mt-4">
          <RetryPolicyPanel />
        </TabsContent>
      </Tabs>

      <ProfileSheet
        initialSubscription={initialSubscription}
        profile={editing}
        open={editOpen}
        onOpenChange={setEditOpen}
        onSaved={() => void load()}
        hasActiveProfile={profiles.some((p) => p.is_default)}
      />
      <PoolSheet open={poolOpen} onOpenChange={setPoolOpen} pool={pool} onReload={loadPool} />
    </div>
  );
}
