"use client";

import * as React from "react";

import Link from "next/link";

import { CheckCircle2Icon, CircleDashedIcon, CircleHelpIcon, InfoIcon } from "lucide-react";

import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

const DISMISS_KEY = "cw.getStarted.dismissed";

type StepStatus = "done" | "todo" | "unknown" | "info";

interface Statuses {
  model: StepStatus;
  scope: StepStatus;
  run: StepStatus;
  integrations: StepStatus;
}

// A failed status call is "unknown", never "todo".
function fromCheck(r: PromiseSettledResult<boolean>): StepStatus {
  if (r.status === "rejected") return "unknown";
  return r.value ? "done" : "todo";
}

async function loadStatuses(): Promise<Statuses> {
  const [profiles, companies, tasks, jira, github] = await Promise.allSettled([
    api.llmProfiles().then((p) => p.some((x) => x.is_default)),
    api.companies().then((c) => c.length > 0),
    api.tasks().then((r) => r.tasks.length > 0),
    api.jira().then((j) => j.configured),
    api.githubToken().then((g) => g.configured),
  ]);
  let integrations: StepStatus;
  if ((jira.status === "fulfilled" && jira.value) || (github.status === "fulfilled" && github.value)) {
    integrations = "done";
  } else if (jira.status === "rejected" || github.status === "rejected") {
    integrations = "unknown";
  } else {
    integrations = "todo";
  }
  return { model: fromCheck(profiles), scope: fromCheck(companies), run: fromCheck(tasks), integrations };
}

function readDismissed(): boolean {
  try {
    return window.localStorage.getItem(DISMISS_KEY) === "1";
  } catch {
    return false;
  }
}

function StatusIcon({ status }: { status: StepStatus }) {
  const cls = "mt-0.5 size-4 shrink-0";
  if (status === "done")
    return <CheckCircle2Icon aria-hidden className={cn(cls, "text-emerald-600 dark:text-emerald-400")} />;
  if (status === "unknown")
    return <CircleHelpIcon aria-hidden className={cn(cls, "text-amber-600 dark:text-amber-400")} />;
  if (status === "info") return <InfoIcon aria-hidden className={cn(cls, "text-muted-foreground")} />;
  return <CircleDashedIcon aria-hidden className={cn(cls, "text-muted-foreground")} />;
}

interface Step {
  id: string;
  status: StepStatus;
  title: string;
  body: string;
  badges?: string[];
  actions: { label: string; href: string; primary?: boolean }[];
}

/**
 * First-run checklist for the dashboard. Status comes from existing list
 * endpoints. The card hides itself once a model is connected and a task exists,
 * or when dismissed (remembered in localStorage).
 */
export function GetStartedCard({ className }: { className?: string }) {
  "use no memo";
  const { t: swt } = useI18n();
  const [dismissed, setDismissed] = React.useState<boolean | null>(null);
  const [statuses, setStatuses] = React.useState<Statuses | null>(null);

  React.useEffect(() => {
    setDismissed(readDismissed());
  }, []);

  React.useEffect(() => {
    if (dismissed !== false) return;
    let alive = true;
    void loadStatuses().then((s) => {
      if (alive) setStatuses(s);
    });
    return () => {
      alive = false;
    };
  }, [dismissed]);

  const hide = () => {
    try {
      window.localStorage.setItem(DISMISS_KEY, "1");
    } catch {
      /* storage unavailable: hide for this visit only */
    }
    setDismissed(true);
  };

  if (dismissed === true) return null;
  if (statuses && statuses.model === "done" && statuses.run === "done") return null;

  if (!statuses) {
    return (
      <Card className={className} aria-busy="true">
        <CardHeader>
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </CardHeader>
        <CardContent className="space-y-3">
          {[0, 1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </CardContent>
      </Card>
    );
  }

  const steps: Step[] = [
    {
      id: "model",
      status: statuses.model,
      title: swt("onboarding.getStartedModelTitle"),
      body: swt("onboarding.getStartedModelBody"),
      actions: [{ label: swt("onboarding.getStartedModelAction"), href: "/system/llm/", primary: true }],
    },
    {
      id: "scope",
      status: statuses.scope,
      title: swt("onboarding.getStartedScopeTitle"),
      body: swt("onboarding.getStartedScopeBody"),
      badges: [swt("onboarding.getStartedRecommended"), swt("onboarding.getStartedScopeOptional")],
      actions: [{ label: swt("onboarding.getStartedScopeAction"), href: "/function/assets/" }],
    },
    {
      id: "approvals",
      status: "info",
      title: swt("onboarding.getStartedApprovalsTitle"),
      body: swt("onboarding.getStartedApprovalsBody"),
      actions: [{ label: swt("onboarding.getStartedApprovalsAction"), href: "/system/intercept/" }],
    },
    {
      id: "run",
      status: statuses.run,
      title: swt("onboarding.getStartedRunTitle"),
      body: swt("onboarding.getStartedRunBody"),
      actions: [
        { label: swt("onboarding.getStartedRunNewTask"), href: "/function/tasks/", primary: true },
        {
          label: swt("onboarding.getStartedRunReview"),
          href: `/chat/?draft=${encodeURIComponent(swt("onboarding.getStartedCodeReviewDraft"))}`,
        },
      ],
    },
    {
      id: "integrations",
      status: statuses.integrations,
      title: swt("onboarding.getStartedIntegrationsTitle"),
      body: swt("onboarding.getStartedIntegrationsBody"),
      actions: [{ label: swt("onboarding.getStartedIntegrationsAction"), href: "/system/settings/" }],
    },
  ];

  const statusText: Record<StepStatus, string> = {
    done: swt("onboarding.getStartedStatusDone"),
    todo: swt("onboarding.getStartedStatusTodo"),
    unknown: swt("onboarding.getStartedStatusUnknown"),
    info: swt("onboarding.getStartedStatusInfo"),
  };
  const requiredDone = [statuses.model, statuses.run].filter((s) => s === "done").length;

  return (
    <Card className={className}>
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <CardTitle>{swt("onboarding.getStartedTitle")}</CardTitle>
            <CardDescription>
              {swt("onboarding.getStartedDescription")}{" "}
              {swt("onboarding.getStartedProgress", { p0: requiredDone, p1: 2 })}
            </CardDescription>
          </div>
          <Button variant="ghost" size="sm" className="shrink-0" onClick={hide}>
            {swt("onboarding.getStartedHide")}
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        <ol aria-label={swt("onboarding.getStartedList")} className="space-y-3">
          {steps.map((step) => (
            <li key={step.id} className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2">
              <StatusIcon status={step.status} />
              <div className="min-w-0 flex-1 basis-60">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                  <span className="text-sm font-medium">{step.title}</span>
                  <span className="text-xs text-muted-foreground">({statusText[step.status]})</span>
                  {step.badges?.map((b) => (
                    <span key={b} className="rounded-md bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                      {b}
                    </span>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground">{step.body}</p>
              </div>
              <div className="flex shrink-0 flex-wrap gap-1.5">
                {step.actions.map((a) => (
                  <Link
                    key={a.href}
                    href={a.href}
                    className={buttonVariants({
                      variant: a.primary && step.status !== "done" ? "default" : "outline",
                      size: "sm",
                    })}
                  >
                    {a.label}
                  </Link>
                ))}
              </div>
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  );
}
