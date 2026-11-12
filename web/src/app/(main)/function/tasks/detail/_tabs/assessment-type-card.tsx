"use client";

import * as React from "react";

import { CircleAlertIcon, ExternalLinkIcon, TagIcon } from "lucide-react";
import { toast } from "sonner";

import { AssessmentTypeBadge, assessmentTypeMeta } from "@/components/assessment-type-badge";
import { AssessmentTypePicker, CodeReviewFields, refError, repositoryError } from "@/components/assessment-type-fields";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { type MessageKey, type TFunction, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { joinRepositoryRef } from "@/lib/code-review-form";
import { shortCommit } from "@/lib/review-change";
import { assessmentTypeOf } from "@/lib/task-workspace";
import type { AssessmentType, CodeReviewMode, CodeTargetChange, Task } from "@/lib/types";

const DEPTH_LABELS: Record<CodeReviewMode, MessageKey> = {
  light: "taskdetail.depthLight",
  balanced: "taskdetail.depthBalanced",
  comprehensive: "taskdetail.depthComprehensive",
};

// Whether the console should offer a type change. The API has no "can change" flag, so this follows
// the documented rule (a task that has never started) from the fields the task already carries; the
// server re-checks and answers 409 when it disagrees.
function typeChangeMode(task: Task): "change" | "reclassify" | null {
  if (task.assessment_type === undefined) return null; // Older backend: no types, nothing to change.
  const legacy = task.assessment_type_explicit === false;
  const finished = ["done", "failed", "timeout"].includes(task.status);
  if (legacy && finished) return "reclassify";
  const unstarted = ["created", "queued", "paused"].includes(task.status) && !task.active && !task.last_activity_unix;
  return unstarted ? "change" : null;
}

// A commit with the branch or tag it was named by, when the change was given by name.
function CommitValue({ name, sha }: { name?: string; sha: string }) {
  return (
    <span className="inline-flex min-w-0 max-w-full items-baseline gap-1.5">
      {name && <span className="truncate">{name}</span>}
      <code className="shrink-0 font-mono" title={sha}>
        {shortCommit(sha)}
      </code>
    </span>
  );
}

// The rows that describe the change a review is scoped to.
function changeRows(change: CodeTargetChange, swt: TFunction) {
  const rows: { label: string; value: React.ReactNode }[] = [];
  if (change.pull_number) {
    const label = swt("prreview.changePull", { p0: change.pull_number });
    rows.push({
      label: swt("prreview.changeLabel"),
      value: /^https:\/\/github\.com\//.test(change.pull_url ?? "") ? (
        <a
          href={change.pull_url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex max-w-full items-center gap-1 text-primary hover:underline"
        >
          <span className="truncate">{label}</span>
          <ExternalLinkIcon className="size-3 shrink-0" aria-hidden="true" />
          <span className="sr-only">{swt("prreview.changeOpenPull")}</span>
        </a>
      ) : (
        label
      ),
    });
  }
  rows.push({
    label: swt("prreview.changeBase"),
    value: <CommitValue name={change.base_ref} sha={change.base_commit} />,
  });
  rows.push({
    label: swt("prreview.changeHead"),
    value: <CommitValue name={change.head_ref} sha={change.head_commit} />,
  });
  rows.push({
    label: swt("prreview.changeFiles"),
    value:
      change.total_files === 1
        ? swt("prreview.changeFilesOne")
        : swt("prreview.changeFilesCount", { p0: change.total_files }),
  });
  return rows;
}

function ChangeTypeDialog({
  task,
  reclassify,
  open,
  onOpenChange,
  onUpdated,
}: {
  task: Task;
  reclassify: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onUpdated: (task: Task) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const current = assessmentTypeOf(task);
  const [type, setType] = React.useState<AssessmentType>(current);
  const [repository, setRepository] = React.useState(task.code_target?.repository ?? "");
  const [refValue, setRefValue] = React.useState(task.code_target?.ref ?? "");
  const [mode, setMode] = React.useState<CodeReviewMode>(
    task.code_target?.mode === "light" || task.code_target?.mode === "comprehensive"
      ? task.code_target.mode
      : "balanced",
  );
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState("");

  // Start from the task's current values each time the dialog opens. The task refreshes while the
  // dialog is open, so it is read through a ref and must not reset what the user is typing.
  const taskRef = React.useRef(task);
  taskRef.current = task;
  React.useEffect(() => {
    if (!open) return;
    const latest = taskRef.current;
    setType(assessmentTypeOf(latest));
    setRepository(latest.code_target?.repository ?? "");
    setRefValue(latest.code_target?.ref ?? "");
    setError("");
  }, [open]);

  const toReview = type === "code_review";
  const invalid = toReview && (repositoryError(repository) !== null || refError(refValue) !== null);
  // Only an ordinary change into a review clones; a historical reclassification never does.
  const cloning = saving && toReview && !reclassify;

  async function save() {
    if (saving || invalid) return;
    setSaving(true);
    setError("");
    try {
      const updated = await api.changeAssessmentType(task.id, {
        assessmentType: type,
        reclassify,
        ...(toReview ? { repository: repository.trim(), ref: refValue.trim(), mode } : {}),
      });
      onUpdated(updated);
      toast.success(swt("taskdetail.typeUpdated"));
      onOpenChange(false);
    } catch (e) {
      const timedOut = e instanceof DOMException && (e.name === "TimeoutError" || e.name === "AbortError");
      setError(timedOut ? swt("taskdetail.typeChangeTimedOut") : (e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{reclassify ? swt("taskdetail.setType") : swt("taskdetail.changeType")}</DialogTitle>
          <DialogDescription>{swt("taskdetail.typeDialogDescription")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          {reclassify && (
            <Alert>
              <CircleAlertIcon />
              <AlertDescription>{swt("taskdetail.reclassifyWarning")}</AlertDescription>
            </Alert>
          )}
          <AssessmentTypePicker value={type} onValueChange={setType} disabled={saving} idPrefix="change-type" />
          {toReview && (
            <CodeReviewFields
              repository={repository}
              onRepositoryChange={setRepository}
              refValue={refValue}
              onRefChange={setRefValue}
              mode={mode}
              onModeChange={setMode}
              disabled={saving}
              idPrefix="change-type-review"
            />
          )}
          {cloning && (
            <Alert aria-live="polite">
              <Spinner />
              <AlertTitle>{swt("taskdetail.typeCloning")}</AlertTitle>
              <AlertDescription>{swt("taskdetail.typeCloningHint")}</AlertDescription>
            </Alert>
          )}
          {error && (
            <Alert variant="destructive" role="alert">
              <CircleAlertIcon />
              <AlertTitle>{swt("taskdetail.typeChangeFailed")}</AlertTitle>
              <AlertDescription className="break-words">{error}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
            {swt("interface.m0063")}
          </Button>
          <Button type="button" disabled={saving || invalid} onClick={() => void save()}>
            {saving && <Spinner data-icon="inline-start" />}
            {saving ? swt("taskdetail.typeSaving") : swt("taskdetail.typeSave")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// AssessmentTypeCard sits at the top of the overview: the task's type and, for a code review, the
// repository it reads, plus the control to change or set the type while the API allows it.
export function AssessmentTypeCard({ task, onUpdated }: { task: Task; onUpdated: (task: Task) => void }) {
  "use no memo";
  const { t: swt } = useI18n();
  const [open, setOpen] = React.useState(false);
  const type = assessmentTypeOf(task);
  const target = task.code_target;
  const change = typeChangeMode(task);
  const notChosen = task.assessment_type_explicit === false;
  const rows: { label: string; value: React.ReactNode }[] = [];
  if (type === "code_review" && target) {
    rows.push({
      label: swt("taskdetail.repositoryLabel"),
      value: /^https:\/\/github\.com\//.test(target.url) ? (
        <a
          href={target.url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex max-w-full items-center gap-1 text-primary hover:underline"
        >
          <span className="truncate">{target.repository}</span>
          <ExternalLinkIcon className="size-3 shrink-0" aria-hidden="true" />
          <span className="sr-only">{swt("taskdetail.openGithub")}</span>
        </a>
      ) : (
        target.repository
      ),
    });
    rows.push({ label: swt("taskdetail.refLabel"), value: target.ref || swt("taskdetail.refDefault") });
    if (target.commit_sha) {
      rows.push({
        label: swt("taskdetail.commitLabel"),
        value: (
          <code className="font-mono" title={target.commit_sha}>
            {target.commit_sha.slice(0, 7)}
          </code>
        ),
      });
    }
    const depth = DEPTH_LABELS[target.mode as CodeReviewMode];
    if (depth) rows.push({ label: swt("taskdetail.depthLabel"), value: swt(depth) });
    if (target.change) rows.push(...changeRows(target.change, swt));
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <TagIcon className="size-4 text-primary" /> {swt("taskdetail.typeCardTitle")}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <AssessmentTypeBadge type={task.assessment_type} />
          <span className="min-w-0 text-sm">{swt(assessmentTypeMeta(type).title)}</span>
          {notChosen && <span className="text-muted-foreground text-xs">{swt("taskdetail.typeNotChosen")}</span>}
          {change && (
            <Button type="button" size="sm" variant="outline" className="ml-auto" onClick={() => setOpen(true)}>
              {notChosen ? swt("taskdetail.setType") : swt("taskdetail.changeType")}
            </Button>
          )}
        </div>
        {change && (
          <p className="text-muted-foreground text-xs">
            {change === "reclassify" ? swt("taskdetail.setTypeHint") : swt("taskdetail.changeTypeHint")}
          </p>
        )}
        {rows.length > 0 && (
          <dl className="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2 lg:grid-cols-4">
            {rows.map((row) => (
              <div key={row.label} className="flex min-w-0 flex-col gap-0.5">
                <dt className="text-muted-foreground text-xs font-medium">{row.label}</dt>
                <dd className="min-w-0 truncate text-sm">{row.value}</dd>
              </div>
            ))}
          </dl>
        )}
        {target?.change?.files_truncated && (
          <p className="text-muted-foreground text-xs">{swt("prreview.changeFilesTruncated")}</p>
        )}
        {target?.change?.diff_truncated && (
          <p className="text-muted-foreground text-xs">{swt("prreview.changeDiffTruncated")}</p>
        )}
      </CardContent>
      {change && (
        <ChangeTypeDialog
          task={task}
          reclassify={change === "reclassify"}
          open={open}
          onOpenChange={setOpen}
          onUpdated={onUpdated}
        />
      )}
    </Card>
  );
}
