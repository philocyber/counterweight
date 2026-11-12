"use client";

import * as React from "react";

import { ExternalLinkIcon, TicketIcon, TriangleAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import type { JiraConfig, JiraProject, JiraProjectMeta, JiraStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

const SEVERITIES = [
  { key: "critical", label: "jira.settingsSeverityCritical" },
  { key: "high", label: "jira.settingsSeverityHigh" },
  { key: "medium", label: "jira.settingsSeverityMedium" },
  { key: "low", label: "jira.settingsSeverityLow" },
  { key: "info", label: "jira.settingsSeverityInfo" },
] as const;

const TOKEN_HELP = "https://id.atlassian.com/manage-profile/security/api-tokens";

function optionName(v: { name?: string; value?: string; id: string }) {
  return [v.name, v.value, v.id].find((x) => !!x) ?? v.id;
}

/**
 * Jira Cloud connection and issue settings. Findings are filed as issues, one
 * epic per task; the API token is entered once and never shown again.
 */
export function JiraIntegrationCard({ className }: { className?: string }) {
  "use no memo";
  const { t: swt } = useI18n();
  const [confirm, confirmDialog] = useConfirm();
  const [status, setStatus] = React.useState<JiraStatus | null>(null);
  const [site, setSite] = React.useState("");
  const [email, setEmail] = React.useState("");
  const [token, setToken] = React.useState("");
  const [editing, setEditing] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [config, setConfig] = React.useState<JiraConfig | null>(null);
  const [projects, setProjects] = React.useState<JiraProject[]>([]);
  const [meta, setMeta] = React.useState<JiraProjectMeta | null>(null);
  const [labels, setLabels] = React.useState("");

  const apply = React.useCallback((next: JiraStatus) => {
    setStatus(next);
    setConfig(next.config);
    setLabels(next.config.labels.join(", "));
    setSite(next.site ?? "");
    setEmail(next.email ?? "");
  }, []);

  React.useEffect(() => {
    api
      .jira()
      .then(apply)
      .catch(() => setStatus(null))
      // "Open Jira settings" links here with #jira; scroll once the settings page has laid out.
      .finally(() => {
        if (window.location.hash === "#jira") document.getElementById("jira")?.scrollIntoView({ block: "start" });
      });
  }, [apply]);

  const configured = !!status?.configured;
  React.useEffect(() => {
    if (!configured) return;
    api
      .jiraProjects()
      .then(setProjects)
      .catch((e) => toast.error((e as Error).message));
  }, [configured]);

  const projectKey = config?.project_key ?? "";
  const issueTypeID = config?.issue_type_id ?? "";
  React.useEffect(() => {
    if (!configured || !projectKey) {
      setMeta(null);
      return;
    }
    api
      .jiraProjectMeta(projectKey, issueTypeID || undefined)
      .then(setMeta)
      .catch((e) => toast.error((e as Error).message));
  }, [configured, projectKey, issueTypeID]);

  const connect = async () => {
    setBusy(true);
    try {
      apply(await api.saveJiraCredentials(site, email, token));
      setToken("");
      setEditing(false);
      toast.success(swt("jira.settingsConnected"));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const disconnect = async () => {
    const ok = await confirm({
      title: swt("confirm.disconnectJiraTitle"),
      description: swt("confirm.disconnectJiraDescription"),
      confirmLabel: swt("confirm.disconnect"),
      destructive: true,
    });
    if (!ok) return;
    setBusy(true);
    try {
      apply(await api.deleteJiraCredentials());
      setProjects([]);
      toast.success(swt("jira.settingsDisconnected"));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!config) return;
    setBusy(true);
    try {
      apply(
        await api.saveJiraConfig({
          ...config,
          labels: labels
            .split(",")
            .map((l) => l.trim())
            .filter(Boolean),
        }),
      );
      toast.success(swt("jira.settingsSaved"));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const update = (patch: Partial<JiraConfig>) => setConfig((c) => (c ? { ...c, ...patch } : c));
  const issueTypes = (meta?.issue_types ?? []).filter((t) => !t.subtask && t.hierarchyLevel === 0);
  const epicTypes = (meta?.issue_types ?? []).filter((t) => t.hierarchyLevel === 1);
  const fields = meta?.fields;
  const showForm = !configured || editing;

  return (
    <Card id="jira" className={cn("scroll-mt-20", className)}>
      {confirmDialog}
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <TicketIcon className="size-4" />
          Jira
        </CardTitle>
        <CardDescription>{swt("jira.settingsDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {configured && (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge variant="secondary">
              {swt("jira.settingsConnectedAs", { p0: status?.account || status?.email })}
            </Badge>
            <span className="text-muted-foreground">
              {status?.site} · {status?.hint}
            </span>
            <div className="ml-auto flex gap-2">
              {!editing && (
                <Button variant="outline" size="sm" disabled={busy} onClick={() => setEditing(true)}>
                  {swt("jira.settingsChange")}
                </Button>
              )}
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => void disconnect()}>
                {swt("jira.settingsDisconnect")}
              </Button>
            </div>
          </div>
        )}

        {showForm && (
          <form
            className="flex flex-col gap-3"
            onSubmit={(e) => {
              e.preventDefault();
              void connect();
            }}
          >
            <div className="grid gap-1.5">
              <Label htmlFor="jira-site">{swt("jira.settingsSiteLabel")}</Label>
              <Input
                id="jira-site"
                placeholder="https://your-company.atlassian.net"
                value={site}
                onChange={(e) => setSite(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="jira-email">{swt("jira.settingsEmailLabel")}</Label>
              <Input
                id="jira-email"
                type="email"
                placeholder="appsec-bot@your-company.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="off"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="jira-token">{swt("jira.settingsTokenLabel")}</Label>
              <Input
                id="jira-token"
                type="password"
                placeholder={
                  configured ? swt("jira.settingsTokenKeepPlaceholder") : swt("jira.settingsTokenPlaceholder")
                }
                value={token}
                onChange={(e) => setToken(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
              <a
                href={TOKEN_HELP}
                target="_blank"
                rel="noreferrer"
                className="inline-flex w-fit items-center gap-1 text-primary text-xs underline-offset-4 hover:underline"
              >
                {swt("jira.settingsCreateToken")} <ExternalLinkIcon className="size-3" />
              </a>
            </div>
            <p className="text-muted-foreground text-xs">{swt("jira.settingsAccountHint")}</p>
            <div className="flex gap-2">
              <Button type="submit" disabled={busy || !site.trim() || !email.trim() || (!configured && !token.trim())}>
                {busy ? swt("jira.settingsConnecting") : swt("jira.settingsConnect")}
              </Button>
              {editing && (
                <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
                  {swt("common.cancel")}
                </Button>
              )}
            </div>
          </form>
        )}

        {configured && config && (
          <div className="flex flex-col gap-4 border-t pt-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="grid gap-1.5">
                <Label htmlFor="jira-project">{swt("jira.settingsDefaultSpace")}</Label>
                <NativeSelect
                  id="jira-project"
                  className="w-full"
                  value={config.project_key}
                  onChange={(e) =>
                    update({
                      project_key: e.target.value,
                      issue_type_id: "",
                      epic_type_id: "",
                      priorities: {},
                      security_level_id: "",
                    })
                  }
                >
                  <NativeSelectOption value="">{swt("jira.settingsNoDefault")}</NativeSelectOption>
                  {projects.map((p) => (
                    <NativeSelectOption key={p.id} value={p.key}>
                      {p.name} ({p.key})
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="jira-type">{swt("jira.settingsDefaultIssueType")}</Label>
                <NativeSelect
                  id="jira-type"
                  className="w-full"
                  value={config.issue_type_id}
                  disabled={!config.project_key}
                  onChange={(e) => update({ issue_type_id: e.target.value, priorities: {}, security_level_id: "" })}
                >
                  <NativeSelectOption value="">{swt("jira.targetChooseIssueType")}</NativeSelectOption>
                  {issueTypes.map((t) => (
                    <NativeSelectOption key={t.id} value={t.id}>
                      {t.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="jira-epic">{swt("jira.settingsEpicType")}</Label>
                <NativeSelect
                  id="jira-epic"
                  className="w-full"
                  value={config.epic_type_id}
                  disabled={!config.project_key}
                  onChange={(e) => update({ epic_type_id: e.target.value })}
                >
                  <NativeSelectOption value="">
                    {epicTypes.length
                      ? swt("jira.settingsEpicAutomatic", { p0: epicTypes[0].name })
                      : swt("jira.settingsNoEpics")}
                  </NativeSelectOption>
                  {epicTypes.map((t) => (
                    <NativeSelectOption key={t.id} value={t.id}>
                      {t.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              {fields && fields.security_levels.length > 0 && (
                <div className="grid gap-1.5">
                  <Label htmlFor="jira-security">{swt("jira.settingsSecurityLevel")}</Label>
                  <NativeSelect
                    id="jira-security"
                    className="w-full"
                    value={config.security_level_id}
                    onChange={(e) => update({ security_level_id: e.target.value })}
                  >
                    <NativeSelectOption value="">{swt("jira.settingsProjectDefault")}</NativeSelectOption>
                    {fields.security_levels.map((l) => (
                      <NativeSelectOption key={l.id} value={l.id}>
                        {optionName(l)}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </div>
              )}
            </div>

            {fields && fields.unsupported_required.length > 0 && (
              <p className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-amber-800 text-sm dark:text-amber-300">
                <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
                {swt("jira.settingsUnsupportedRequired", { p0: fields.unsupported_required.join(", ") })}
              </p>
            )}

            {fields && (
              <div className="overflow-x-auto rounded-lg border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/60 text-left text-[11px] text-muted-foreground uppercase tracking-wide">
                    <tr>
                      <th className="px-3 py-2 font-semibold">{swt("jira.settingsColSeverity")}</th>
                      <th className="px-3 py-2 font-semibold">{swt("jira.settingsColPriority")}</th>
                      <th className="px-3 py-2 font-semibold">{swt("jira.settingsColFixWithin")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {SEVERITIES.map((s) => (
                      <tr key={s.key} className="border-t">
                        <td className="px-3 py-2">{swt(s.label)}</td>
                        <td className="px-3 py-1.5">
                          <NativeSelect
                            size="sm"
                            aria-label={swt("jira.settingsPriorityAria", { p0: swt(s.label) })}
                            value={config.priorities[s.key] ?? ""}
                            disabled={fields.priorities.length === 0}
                            onChange={(e) => update({ priorities: { ...config.priorities, [s.key]: e.target.value } })}
                          >
                            <NativeSelectOption value="">{swt("jira.settingsProjectDefault")}</NativeSelectOption>
                            {fields.priorities.map((p) => (
                              <NativeSelectOption key={p.id} value={p.id}>
                                {optionName(p)}
                              </NativeSelectOption>
                            ))}
                          </NativeSelect>
                        </td>
                        <td className="px-3 py-1.5">
                          <Input
                            type="number"
                            min={0}
                            max={3650}
                            className="h-7 w-24"
                            aria-label={swt("jira.settingsDaysAria", { p0: swt(s.label) })}
                            disabled={!fields.has_due_date}
                            value={config.sla_days[s.key] ?? 0}
                            onChange={(e) =>
                              update({ sla_days: { ...config.sla_days, [s.key]: Number(e.target.value) || 0 } })
                            }
                          />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <p className="border-t px-3 py-2 text-muted-foreground text-xs">
                  {fields.has_due_date ? swt("jira.settingsDueDateOn") : swt("jira.settingsDueDateOff")}
                  {fields.priorities.length === 0 && ` ${swt("jira.settingsNoPriorityField")}`}
                  {` ${swt("jira.settingsPrioritiesEverySpace")}`}
                </p>
              </div>
            )}

            <div className="grid gap-1.5">
              <Label htmlFor="jira-labels">{swt("jira.settingsExtraLabels")}</Label>
              <Input
                id="jira-labels"
                placeholder="appsec, team-payments"
                value={labels}
                onChange={(e) => setLabels(e.target.value)}
              />
              <span className="text-muted-foreground text-xs">{swt("jira.settingsExtraLabelsHint")}</span>
            </div>

            <Label className="flex items-start gap-3 font-normal">
              <Switch checked={config.sync} onCheckedChange={(v) => update({ sync: v })} className="mt-0.5" />
              <span className="flex flex-col gap-0.5">
                <span className="text-sm">{swt("jira.settingsSyncTitle")}</span>
                <span className="text-muted-foreground text-xs">{swt("jira.settingsSyncHint")}</span>
              </span>
            </Label>

            <div>
              <Button onClick={() => void save()} disabled={busy}>
                {swt("jira.settingsSave")}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
