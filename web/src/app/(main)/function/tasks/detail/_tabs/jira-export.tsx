"use client";

import * as React from "react";

import Link from "next/link";

import { ExternalLinkIcon, LockIcon, RefreshCwIcon, TicketIcon, TriangleAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import type {
  JiraLink,
  JiraProject,
  JiraProjectMeta,
  JiraTarget,
  JiraTaskExport,
  JiraTaskLinks,
  ReportView,
} from "@/lib/types";
import { cn } from "@/lib/utils";

const CATEGORY_TONE: Record<string, string> = {
  done: "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  indeterminate: "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  new: "border-border bg-muted/60 text-muted-foreground",
};

export type JiraState = ReturnType<typeof useJiraLinks>;

/** useJiraLinks loads the Jira issues filed for a task. */
export function useJiraLinks(taskId: string) {
  const { t: swt } = useI18n();
  const [links, setLinks] = React.useState<JiraTaskLinks | null>(null);
  const load = React.useCallback(
    async (refresh = false) => {
      if (!taskId) return;
      await api
        .jiraTaskLinks(taskId, refresh)
        .then(setLinks)
        .catch(() => setLinks(null));
    },
    [taskId],
  );
  React.useEffect(() => {
    void load();
  }, [load]);

  const exportFinding = React.useCallback(
    async (findingId: number, target?: JiraTarget) => {
      try {
        const out = await api.jiraExportFinding(findingId, target);
        toast.success(
          out.issue.created
            ? swt("jira.toastCreated", { p0: out.issue.key })
            : swt("jira.toastAlreadyFiled", { p0: out.issue.key }),
        );
        await load();
        return true;
      } catch (e) {
        toast.error((e as Error).message);
        return false;
      }
    },
    [load, swt],
  );
  return { links, reload: load, exportFinding };
}

/** JiraIssueBadge shows a filed issue with its last known Jira status. */
export function JiraIssueBadge({ link, interactive = true }: { link: JiraLink; interactive?: boolean }) {
  const className = cn(
    "inline-flex items-center gap-1.5 rounded-md border px-1.5 py-0.5 font-medium text-[11px]",
    CATEGORY_TONE[link.status_category ?? "new"] ?? CATEGORY_TONE.new,
  );
  const content = (
    <>
      <TicketIcon className="size-3" />
      {link.key}
      {link.status_name && <span className="font-normal opacity-80">· {link.status_name}</span>}
    </>
  );
  if (!interactive) return <span className={className}>{content}</span>;
  return (
    <a href={link.url} target="_blank" rel="noreferrer" className={cn(className, "hover:underline")}>
      {content}
      <ExternalLinkIcon className="size-3" />
    </a>
  );
}

function optionName(v: { name?: string; value?: string; id: string }) {
  return [v.name, v.value].find((x) => !!x) ?? v.id;
}

/** initialTarget is the task's epic target, or the defaults from Settings. */
function initialTarget(links: JiraTaskLinks): JiraTarget {
  if (links.epic) {
    return {
      project_key: links.epic.project_key,
      issue_type_id: links.epic.issue_type_id ?? "",
      security_level_id: links.epic.security_level_id ?? "",
    };
  }
  return {
    project_key: links.default_project,
    issue_type_id: links.default_issue_type,
    security_level_id: links.default_security_level,
  };
}

/**
 * JiraTargetPicker chooses the Jira space, issue type and security level of an
 * export. Once a task has an epic its space is fixed, so one assessment never
 * spreads across spaces.
 */
function JiraTargetPicker({
  value,
  onChange,
  locked,
  onReady,
}: {
  value: JiraTarget;
  onChange: (next: JiraTarget) => void;
  locked: boolean;
  onReady: (ready: boolean) => void;
}) {
  const { t: swt } = useI18n();
  const [projects, setProjects] = React.useState<JiraProject[]>([]);
  const [meta, setMeta] = React.useState<JiraProjectMeta | null>(null);

  React.useEffect(() => {
    if (locked) return;
    api
      .jiraProjects()
      .then(setProjects)
      .catch((e) => toast.error((e as Error).message));
  }, [locked]);

  React.useEffect(() => {
    if (!value.project_key) {
      setMeta(null);
      return;
    }
    api
      .jiraProjectMeta(value.project_key, value.issue_type_id || undefined)
      .then(setMeta)
      .catch((e) => toast.error((e as Error).message));
  }, [value.project_key, value.issue_type_id]);

  const issueTypes = (meta?.issue_types ?? []).filter((t) => !t.subtask && t.hierarchyLevel === 0);
  const hasEpics = (meta?.issue_types ?? []).some((t) => t.hierarchyLevel === 1);
  const fields = meta?.fields;
  const blocked = (fields?.unsupported_required.length ?? 0) > 0;
  const ready = !!value.project_key && !!value.issue_type_id && !!fields && !blocked;
  React.useEffect(() => onReady(ready), [ready, onReady]);

  return (
    <div className="flex flex-col gap-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="grid gap-1.5">
          <Label htmlFor="jira-target-space">{swt("jira.targetSpaceLabel")}</Label>
          {locked ? (
            <span className="flex h-8 items-center gap-1.5 rounded-lg border bg-muted/40 px-2.5 text-sm">
              <LockIcon className="size-3.5 text-muted-foreground" />
              {value.project_key}
            </span>
          ) : (
            <NativeSelect
              id="jira-target-space"
              className="w-full"
              value={value.project_key}
              onChange={(e) => onChange({ project_key: e.target.value, issue_type_id: "", security_level_id: "" })}
            >
              <NativeSelectOption value="">{swt("jira.targetChooseSpace")}</NativeSelectOption>
              {projects.map((p) => (
                <NativeSelectOption key={p.id} value={p.key}>
                  {p.name} ({p.key})
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="jira-target-type">{swt("jira.targetIssueTypeLabel")}</Label>
          <NativeSelect
            id="jira-target-type"
            className="w-full"
            value={value.issue_type_id}
            disabled={!value.project_key}
            onChange={(e) => onChange({ ...value, issue_type_id: e.target.value })}
          >
            <NativeSelectOption value="">{swt("jira.targetChooseIssueType")}</NativeSelectOption>
            {issueTypes.map((t) => (
              <NativeSelectOption key={t.id} value={t.id}>
                {t.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        {fields && fields.security_levels.length > 0 && (
          <div className="grid gap-1.5 sm:col-span-2">
            <Label htmlFor="jira-target-security">{swt("jira.targetVisibilityLabel")}</Label>
            <NativeSelect
              id="jira-target-security"
              className="w-full"
              value={value.security_level_id}
              onChange={(e) => onChange({ ...value, security_level_id: e.target.value })}
            >
              <NativeSelectOption value="">{swt("jira.targetDefaultVisibility")}</NativeSelectOption>
              {fields.security_levels.map((l) => (
                <NativeSelectOption key={l.id} value={l.id}>
                  {optionName(l)}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        )}
      </div>
      {locked && (
        <p className="text-muted-foreground text-xs">{swt("jira.targetLockedNote", { p0: value.project_key })}</p>
      )}
      {meta && !hasEpics && <p className="text-muted-foreground text-xs">{swt("jira.targetNoEpicType")}</p>}
      {blocked && (
        <p className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-amber-800 text-sm dark:text-amber-300">
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
          {swt("jira.targetUnsupportedRequired", { p0: fields?.unsupported_required.join(", ") })}
        </p>
      )}
    </div>
  );
}

/**
 * ConnectJiraDialog explains the integration when Jira is not connected yet and
 * leads to Settings, so the export actions are discoverable before setup.
 */
function ConnectJiraDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t: swt } = useI18n();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{swt("jira.connectTitle")}</DialogTitle>
          <DialogDescription>{swt("jira.connectDescription")}</DialogDescription>
        </DialogHeader>
        <p className="text-sm">{swt("jira.connectBody")}</p>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {swt("jira.connectNotNow")}
          </Button>
          <Button asChild>
            <Link href="/system/settings#jira">
              <TicketIcon /> {swt("jira.connectOpenSettings")}
            </Link>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * JiraFindingControl files one finding, or links the issue already filed.
 * compact renders a small button for dense finding rows.
 */
export function JiraFindingControl({
  findingId,
  links,
  onExport,
  compact = false,
}: {
  findingId: number;
  links: JiraTaskLinks | null;
  onExport: (id: number, target?: JiraTarget) => Promise<boolean>;
  compact?: boolean;
}) {
  const { t: swt } = useI18n();
  const [busy, setBusy] = React.useState(false);
  const [open, setOpen] = React.useState(false);
  const [connect, setConnect] = React.useState(false);
  const [target, setTarget] = React.useState<JiraTarget | null>(null);
  const [ready, setReady] = React.useState(false);
  const link = links?.configured ? links.findings[String(findingId)] : undefined;
  if (link) return <JiraIssueBadge link={link} />;

  const send = async (t?: JiraTarget) => {
    setBusy(true);
    const ok = await onExport(findingId, t);
    setBusy(false);
    if (ok) setOpen(false);
  };

  return (
    <>
      <Button
        size="sm"
        variant={compact ? "ghost" : "outline"}
        className={compact ? "h-7 shrink-0 px-2 text-xs" : undefined}
        title={swt("jira.findingSendTitle")}
        disabled={busy}
        onClick={() => {
          if (!links?.configured) {
            setConnect(true);
            return;
          }
          // With an epic the space is known: file in one click.
          if (links.epic) {
            void send();
            return;
          }
          setTarget(initialTarget(links));
          setOpen(true);
        }}
      >
        <TicketIcon /> {busy ? swt("jira.findingCreating") : compact ? "Jira" : swt("jira.findingSend")}
      </Button>
      <ConnectJiraDialog open={connect} onOpenChange={setConnect} />
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{swt("jira.sendDialogTitle")}</DialogTitle>
            <DialogDescription>{swt("jira.sendDialogDescription")}</DialogDescription>
          </DialogHeader>
          {target && <JiraTargetPicker value={target} onChange={setTarget} locked={false} onReady={setReady} />}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {swt("common.cancel")}
            </Button>
            <Button disabled={busy || !ready || !target} onClick={() => target && void send(target)}>
              <TicketIcon /> {busy ? swt("jira.findingCreating") : swt("jira.sendCreateIssue")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

/** JiraTaskExportButton files the task as an epic with its findings as child issues. */
export function JiraTaskExportButton({
  taskId,
  view,
  links,
  reload,
}: {
  taskId: string;
  view?: ReportView | null; // loaded on open when the page has no report view
  links: JiraTaskLinks | null;
  reload: (refresh?: boolean) => Promise<void>;
}) {
  const { t: swt, rich } = useI18n();
  const [open, setOpen] = React.useState(false);
  const [connect, setConnect] = React.useState(false);
  const [loaded, setLoaded] = React.useState<ReportView | null>(null);
  const [scope, setScope] = React.useState<"open" | "all">("open");
  const [busy, setBusy] = React.useState(false);
  const [result, setResult] = React.useState<JiraTaskExport | null>(null);
  const [target, setTarget] = React.useState<JiraTarget | null>(null);
  const [ready, setReady] = React.useState(false);
  if (!links?.configured) {
    return (
      <>
        <Button size="sm" variant="outline" onClick={() => setConnect(true)}>
          <TicketIcon /> {swt("jira.exportButton")}
        </Button>
        <ConnectJiraDialog open={connect} onOpenChange={setConnect} />
      </>
    );
  }

  const findings = (view ?? loaded)?.findings ?? [];
  const openCount = findings.filter((f) => !!f.next_action).length;
  const filed = Object.keys(links.findings).length;
  const count = scope === "open" ? openCount : findings.length;

  const run = async () => {
    if (!target) return;
    setBusy(true);
    try {
      const out = await api.jiraExportTask(taskId, scope, target);
      setResult(out);
      const created = out.results.filter((r) => r.issue?.created).length;
      const failed = out.results.filter((r) => r.error).length;
      if (failed) toast.error(swt("jira.toastFailedCount", { p0: failed }));
      else toast.success(swt("jira.toastFiledCount", { p0: created }));
      await reload();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  let body: React.ReactNode;
  if (result) {
    body = (
      <div className="flex max-h-80 flex-col gap-2 overflow-y-auto text-sm">
        {result.epic && (
          <p className="flex items-center gap-2">
            {rich("jira.exportEpicResult", { p0: <JiraIssueBadge link={result.epic} /> })}
          </p>
        )}
        <ul className="flex flex-col divide-y rounded-lg border">
          {result.results.map((r) => (
            <li key={r.finding_id} className="flex items-center justify-between gap-3 px-3 py-2">
              <span className="flex min-w-0 items-center gap-2">
                <StatusBadge domain="severity" value={r.severity} dot />
                <span className="truncate">{r.title || swt("jira.exportFindingFallback", { p0: r.finding_id })}</span>
              </span>
              <span className="shrink-0 text-xs">
                {r.issue && <JiraIssueBadge link={r.issue} />}
                {r.skipped && <span className="text-muted-foreground">{swt("jira.exportClosedNotFiled")}</span>}
                {r.error && <span className="text-destructive">{r.error}</span>}
              </span>
            </li>
          ))}
        </ul>
      </div>
    );
  } else {
    body = (
      <div className="flex flex-col gap-4">
        {links.epic && (
          <p className="flex items-center gap-2 text-sm">
            {rich("jira.exportEpicFiled", { p0: <JiraIssueBadge link={links.epic} />, p1: filed })}
          </p>
        )}
        {target && <JiraTargetPicker value={target} onChange={setTarget} locked={!!links.epic} onReady={setReady} />}
        <RadioGroup
          aria-label={swt("jira.exportScopeAria")}
          value={scope}
          onValueChange={(v) => setScope(v as "open" | "all")}
          className="flex flex-col gap-2"
        >
          {[
            {
              value: "open",
              title: swt("jira.exportScopeOpenTitle", { p0: openCount }),
              hint: swt("jira.exportScopeOpenHint"),
            },
            {
              value: "all",
              title: swt("jira.exportScopeAllTitle", { p0: findings.length }),
              hint: swt("jira.exportScopeAllHint"),
            },
          ].map((o) => (
            <Label
              key={o.value}
              htmlFor={`jira-scope-${o.value}`}
              className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 font-normal has-[[data-state=checked]]:border-primary/60 has-[[data-state=checked]]:bg-primary/5"
            >
              <RadioGroupItem id={`jira-scope-${o.value}`} value={o.value} className="mt-0.5" />
              <span className="flex flex-col gap-1">
                <span className="font-medium text-sm">{o.title}</span>
                <span className="text-muted-foreground text-xs">{o.hint}</span>
              </span>
            </Label>
          ))}
        </RadioGroup>
        <p className="text-muted-foreground text-xs">{swt("jira.exportRedactionNote")}</p>
      </div>
    );
  }

  return (
    <>
      <Button
        size="sm"
        variant="outline"
        onClick={() => {
          setResult(null);
          setTarget(initialTarget(links));
          setOpen(true);
          if (!view) {
            api
              .reportView(taskId)
              .then(setLoaded)
              .catch(() => setLoaded(null));
          }
        }}
      >
        <TicketIcon /> {links.epic ? `Jira ${links.epic.key}` : swt("jira.exportButton")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{swt("jira.exportDialogTitle")}</DialogTitle>
            <DialogDescription>{swt("jira.exportDialogDescription")}</DialogDescription>
          </DialogHeader>
          {body}
          <DialogFooter>
            {result ? (
              <Button onClick={() => setOpen(false)}>{swt("jira.exportDone")}</Button>
            ) : (
              <>
                {links.epic && (
                  <Button variant="ghost" onClick={() => void reload(true)} disabled={busy}>
                    <RefreshCwIcon /> {swt("jira.exportRefreshStatuses")}
                  </Button>
                )}
                <Button variant="outline" onClick={() => setOpen(false)}>
                  {swt("common.cancel")}
                </Button>
                <Button onClick={() => void run()} disabled={busy || !ready || count === 0}>
                  <TicketIcon /> {busy ? swt("jira.exportFiling") : swt("jira.exportFileFindings", { p0: count })}
                </Button>
              </>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
