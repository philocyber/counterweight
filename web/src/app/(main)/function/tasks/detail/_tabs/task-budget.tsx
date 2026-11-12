"use client";

import * as React from "react";

import Link from "next/link";

import { CircleAlertIcon, PlayIcon } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Spinner } from "@/components/ui/spinner";
import { type TFunction, useI18n } from "@/i18n";
import { getIntlLocale } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { Task, TaskBudget } from "@/lib/types";
import { cn } from "@/lib/utils";

type Hold = NonNullable<Task["approval_state"]>["holds"][number];

function compact(n: number): string {
  return new Intl.NumberFormat(getIntlLocale(), { notation: "compact", maximumFractionDigits: 1 }).format(n);
}

function money(n: number): string {
  return new Intl.NumberFormat(getIntlLocale(), { minimumFractionDigits: 2, maximumFractionDigits: 4 }).format(n);
}

function hasLimit(budget: TaskBudget): boolean {
  return budget.token_budget > 0 || (budget.cost_budget !== null && budget.cost_budget !== undefined);
}

// One line with the figures a banner and the indicator share.
function figures(budget: TaskBudget, t: TFunction): string[] {
  const out: string[] = [];
  if (budget.token_budget > 0) {
    out.push(t("budget.tokensOfLimit", { p0: compact(budget.tokens_used), p1: compact(budget.token_budget) }));
  } else {
    out.push(t("budget.tokensUsed", { p0: compact(budget.tokens_used) }));
  }
  if (budget.cost_budget !== null && budget.cost_budget !== undefined) {
    out.push(
      t("budget.costOfLimit", {
        p0: money(budget.estimated_cost),
        p1: money(budget.cost_budget),
        p2: budget.currency,
      }),
    );
  } else if (budget.estimated_cost > 0) {
    out.push(t("budget.costOnly", { p0: money(budget.estimated_cost), p1: budget.currency }));
  }
  return out;
}

// Small dialog that changes a task's budget. Raising the budget never resumes a paused task.
function BudgetDialog({
  open,
  onOpenChange,
  taskId,
  budget,
  raise,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  taskId: string;
  budget: TaskBudget;
  raise: boolean;
  onSaved: () => void;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const currentCost = budget.cost_budget === null || budget.cost_budget === undefined ? "" : String(budget.cost_budget);
  const [tokens, setTokens] = React.useState("");
  const [cost, setCost] = React.useState("");
  const [currency, setCurrency] = React.useState("USD");
  const [error, setError] = React.useState("");
  const [saving, setSaving] = React.useState(false);
  // null until the profiles load; a cost budget needs a profile with prices.
  const [priced, setPriced] = React.useState<boolean | null>(null);
  const showCost = currentCost !== "" || budget.estimated_cost > 0 || priced === true;

  // Start from the task's current values every time the dialog opens.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only reset on open, not on every poll of the task.
  React.useEffect(() => {
    if (!open) return;
    setTokens(String(budget.token_budget));
    setCost(currentCost);
    setCurrency(budget.currency || "USD");
    setError("");
    api
      .llmProfiles()
      .then((items) =>
        setPriced(items.some((p) => p.price_input_per_million != null && p.price_output_per_million != null)),
      )
      .catch(() => setPriced(null));
  }, [open]);

  const save = async () => {
    const body: { token_budget?: number; cost_budget?: number; cost_currency?: string } = {};
    if (tokens.trim() !== "" && tokens.trim() !== String(budget.token_budget)) {
      const n = Number(tokens);
      if (!Number.isInteger(n) || n < 0) return setError(swt("budget.invalidTokens"));
      body.token_budget = n;
    }
    if (showCost && cost.trim() !== "" && cost.trim() !== currentCost) {
      const n = Number(cost);
      if (!Number.isFinite(n) || n < 0) return setError(swt("budget.invalidCost"));
      body.cost_budget = n;
    }
    if (showCost && currency.trim().toUpperCase() !== (budget.currency || "USD")) {
      if (!/^[A-Za-z]{3}$/.test(currency.trim())) return setError(swt("budget.invalidCurrency"));
      body.cost_currency = currency.trim().toUpperCase();
    }
    if (Object.keys(body).length === 0) return setError(swt("budget.dialogNothingChanged"));
    setSaving(true);
    setError("");
    try {
      await api.setTaskBudget(taskId, body);
      toast.success(budget.paused_for_budget ? swt("budget.dialogSavedPaused") : swt("budget.dialogSaved"));
      onOpenChange(false);
      onSaved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{raise ? swt("budget.dialogRaiseTitle") : swt("budget.dialogTitle")}</DialogTitle>
          <DialogDescription>
            {swt("budget.dialogDescription", { p0: compact(budget.tokens_used) })}
            {budget.estimated_cost > 0 &&
              ` ${swt("budget.dialogCostUsed", { p0: money(budget.estimated_cost), p1: budget.currency })}`}
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-2">
            <Label htmlFor="budget-tokens">{swt("budget.tokenBudgetLabel")}</Label>
            <Input
              id="budget-tokens"
              type="number"
              min={0}
              className="w-48"
              value={tokens}
              disabled={saving}
              onChange={(e) => setTokens(e.target.value)}
            />
          </div>
          {showCost && (
            <div className="flex flex-wrap gap-4">
              <div className="grid gap-2">
                <Label htmlFor="budget-cost">{swt("budget.costBudgetLabel")}</Label>
                <Input
                  id="budget-cost"
                  type="number"
                  min={0}
                  step="any"
                  className="w-48"
                  value={cost}
                  disabled={saving}
                  onChange={(e) => setCost(e.target.value)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="budget-currency">{swt("budget.currencyLabel")}</Label>
                <Input
                  id="budget-currency"
                  maxLength={3}
                  className="w-24 uppercase"
                  value={currency}
                  disabled={saving}
                  onChange={(e) => setCurrency(e.target.value)}
                />
              </div>
            </div>
          )}
          {!showCost && priced === false && (
            <p className="text-muted-foreground text-xs">
              {swt("budget.costNeedsPrices")}{" "}
              <Link href="/system/llm/" className="font-medium underline underline-offset-2">
                {swt("budget.costNeedsPricesLink")}
              </Link>
            </p>
          )}
          {error && (
            <Alert variant="destructive">
              <CircleAlertIcon />
              <AlertDescription className="break-words">{error}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
            {swt("budget.dialogCancel")}
          </Button>
          <Button type="button" disabled={saving} onClick={() => void save()}>
            {saving && <Spinner data-icon="inline-start" />}
            {swt("budget.dialogSave")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// Header indicator: tokens and cost against the limits, a thin bar, and the state in words.
// A task with a budget object but no limit gets a low-emphasis "Set budget" control instead.
// Renders nothing on a server that does not report budgets.
export function TaskBudgetIndicator({
  taskId,
  budget,
  onChanged,
}: {
  taskId: string;
  budget?: TaskBudget;
  onChanged: () => void;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [editing, setEditing] = React.useState(false);
  if (!budget) return null;
  if (!hasLimit(budget)) {
    return (
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground text-xs">
        {budget.tokens_used > 0 && (
          <span className="tabular-nums">{swt("budget.tokensUsed", { p0: compact(budget.tokens_used) })}</span>
        )}
        <button type="button" className="hover:text-foreground hover:underline" onClick={() => setEditing(true)}>
          {swt("budget.setBudget")}
        </button>
        <BudgetDialog
          open={editing}
          onOpenChange={setEditing}
          taskId={taskId}
          budget={budget}
          raise={false}
          onSaved={onChanged}
        />
      </div>
    );
  }
  const percent = Math.max(0, Math.min(100, budget.percent_used));
  const reached = budget.state === "reached";
  const warning = budget.state === "warning";
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-muted-foreground text-xs">
      <span className="font-medium text-foreground">{swt("budget.indicatorLabel")}</span>
      <span className="tabular-nums">{figures(budget, swt).join(" · ")}</span>
      <Progress
        value={percent}
        aria-label={swt("budget.indicatorLabel")}
        className={cn(
          "w-24 shrink-0",
          warning && "[&>[data-slot=progress-indicator]]:bg-amber-500",
          reached && "[&>[data-slot=progress-indicator]]:bg-destructive",
        )}
      />
      <Badge
        variant={reached ? "destructive" : "outline"}
        className={cn(warning && "text-amber-700 dark:text-amber-400")}
      >
        {reached
          ? swt("budget.stateReached")
          : warning
            ? swt("budget.stateWarning", { p0: Math.round(budget.percent_used) })
            : swt("budget.stateOk")}
      </Badge>
      {budget.unknown_cost && <span>{swt("budget.costUnknown")}</span>}
      <button type="button" className="font-medium text-primary hover:underline" onClick={() => setEditing(true)}>
        {swt("budget.editBudget")}
      </button>
      <BudgetDialog
        open={editing}
        onOpenChange={setEditing}
        taskId={taskId}
        budget={budget}
        raise={false}
        onSaved={onChanged}
      />
    </div>
  );
}

// Banner for a task paused because its budget was reached, or whose budget was raised but which has not
// been resumed yet. Raising the budget does not resume the task, so the banner offers both steps.
export function TaskBudgetBanner({
  taskId,
  budget,
  hold,
  onChanged,
}: {
  taskId: string;
  budget?: TaskBudget;
  hold?: Hold;
  onChanged: () => void;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [raising, setRaising] = React.useState(false);
  const [resuming, setResuming] = React.useState(false);
  const [error, setError] = React.useState("");
  if (!hold && !budget?.paused_for_budget) return null;
  const reached = !budget || budget.state === "reached";

  const resume = async () => {
    setResuming(true);
    setError("");
    try {
      await api.controlTask(taskId, "resume");
      onChanged();
    } catch (e) {
      setError(swt("budget.resumeFailed", { p0: (e as Error).message }));
    } finally {
      setResuming(false);
    }
  };

  return (
    <Alert className="border-amber-500/40 bg-amber-500/10" role="alert">
      <CircleAlertIcon />
      <AlertTitle>{reached ? swt("budget.pausedTitle") : swt("budget.raisedTitle")}</AlertTitle>
      <AlertDescription className="space-y-2">
        {reached ? (
          <>
            <p className="break-words">{hold?.reason || swt("budget.pausedFallback")}</p>
            {budget && <p className="tabular-nums">{figures(budget, swt).join(" · ")}</p>}
            <p>{swt("budget.pausedHint")}</p>
          </>
        ) : (
          <>
            {budget && <p className="tabular-nums">{figures(budget, swt).join(" · ")}</p>}
            <p>{swt("budget.raisedBody")}</p>
          </>
        )}
        <div className="flex flex-wrap items-center gap-2">
          {budget && (
            <Button size="sm" variant={reached ? "default" : "outline"} onClick={() => setRaising(true)}>
              {swt("budget.raiseAction")}
            </Button>
          )}
          {!reached && (
            <Button size="sm" onClick={() => void resume()} disabled={resuming}>
              {resuming ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
              {swt("budget.resumeAction")}
            </Button>
          )}
        </div>
        {error && <p className="break-words text-destructive">{error}</p>}
      </AlertDescription>
      {budget && (
        <BudgetDialog
          open={raising}
          onOpenChange={setRaising}
          taskId={taskId}
          budget={budget}
          raise
          onSaved={onChanged}
        />
      )}
    </Alert>
  );
}
