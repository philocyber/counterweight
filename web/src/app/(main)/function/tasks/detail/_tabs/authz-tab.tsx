"use client";

import * as React from "react";

import Link from "next/link";

import { CheckIcon, CircleAlertIcon, CircleCheckIcon, InfoIcon, ShieldCheckIcon, XIcon } from "lucide-react";
import { toast } from "sonner";

import { EmptyState } from "@/components/console/empty-state";
import { ErrorState } from "@/components/error-state";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { usePolling } from "@/hooks/use-polling";
import { useI18n } from "@/i18n";
import { api, isNotAvailable } from "@/lib/api";
import { policyTooLarge, starterPolicy } from "@/lib/authz";
import { emptyDocument, policyText } from "@/lib/authz-policy";
import type { AuthzDocument, AuthzPolicyCheck, TaskAuthz } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AccountsCard } from "./authz-accounts";
import { MatrixEditor } from "./authz-matrix-editor";
import { AuthzResults } from "./authz-results";

// The server's verdict on the saved policy. Its error sentences are shown as written.
function PolicyCheck({ check, stale }: { check: AuthzPolicyCheck | null; stale: boolean }) {
  "use no memo";
  const { t: swt } = useI18n();
  if (!check) return <p className="text-muted-foreground text-sm">{swt("authz.checkNone")}</p>;
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      {check.ok ? (
        <p className="flex items-start gap-2 text-emerald-800 text-sm dark:text-emerald-400">
          <CircleCheckIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <span>{swt("authz.checkOk", { p0: (check.personas ?? []).length, p1: check.cases, p2: check.checks })}</span>
        </p>
      ) : (
        <Alert variant="destructive">
          <CircleAlertIcon />
          <AlertTitle>{swt("authz.checkProblemsTitle")}</AlertTitle>
          <AlertDescription>
            <ul className="list-disc pl-4">
              {(check.errors ?? []).map((message, index) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: server sentences are not unique and never reorder
                <li key={index}>{message}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {stale && <p className="text-muted-foreground text-xs">{swt("authz.checkStale")}</p>}
    </div>
  );
}

// The three steps of the check, in order, each with where it stands. A step leads to its section.
function Steps({
  label,
  items,
}: {
  label: string;
  // failed marks a step whose last outcome needs attention, such as a run that found a violation.
  items: { target: string; title: string; status: string; done: boolean; failed?: boolean }[];
}) {
  "use no memo";
  return (
    <ol aria-label={label} className="grid min-w-0 gap-2 sm:grid-cols-3">
      {items.map((item, index) => (
        <li key={item.target} className="min-w-0">
          <button
            type="button"
            className="flex w-full min-w-0 items-start gap-3 rounded-lg border bg-card px-3 py-2.5 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => document.getElementById(item.target)?.scrollIntoView({ behavior: "smooth", block: "start" })}
          >
            <span
              className={cn(
                "mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border font-medium text-xs",
                item.done && "border-emerald-700 bg-emerald-700 text-white dark:border-emerald-500 dark:bg-emerald-500",
                item.failed && "border-destructive bg-destructive text-white",
              )}
              aria-hidden="true"
            >
              {item.done && <CheckIcon className="size-3.5" />}
              {!item.done && item.failed && <XIcon className="size-3.5" />}
              {!item.done && !item.failed && index + 1}
            </span>
            <span className="flex min-w-0 flex-col">
              <span className="font-medium text-sm">{item.title}</span>
              <span className="truncate text-muted-foreground text-xs">{item.status}</span>
            </span>
          </button>
        </li>
      ))}
    </ol>
  );
}

// What the matrix starts from when no policy is saved yet.
const NO_POLICY = emptyDocument();

// Authorization check of a task: the test accounts, the policy and the runs of the check.
export function AuthzTab({ taskId }: { taskId: string }) {
  "use no memo";
  const { t: swt, fmt } = useI18n();
  const [data, setData] = React.useState<TaskAuthz | null>(null);
  const [unavailable, setUnavailable] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  // The policy text while it differs from what was loaded; null shows the saved policy.
  const [draft, setDraft] = React.useState<string | null>(null);
  // The document behind the draft while it was edited in the matrix. A change typed into the text
  // clears it: only the server can say what that text means.
  const [draftDoc, setDraftDoc] = React.useState<AuthzDocument | null>(null);
  // The view the user picked; until then the matrix is shown whenever it can be.
  const [picked, setPicked] = React.useState<"matrix" | "text" | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [starting, setStarting] = React.useState(false);
  // Counts the changes made from this page. An answer requested before a change may predate it, so
  // it is not used and the state is asked for again.
  const changes = React.useRef(0);
  const latest = React.useRef({ draft, draftDoc });
  latest.current = { draft, draftDoc };

  const load = React.useCallback(
    async function fetchState(): Promise<void> {
      const at = changes.current;
      try {
        const next = await api.taskAuthz(taskId);
        if (at !== changes.current) return fetchState();
        setData(next);
        setUnavailable(false);
        setError(null);
      } catch (e) {
        if (at !== changes.current) return fetchState();
        if (isNotAvailable(e)) setUnavailable(true);
        else setError((e as Error).message);
      }
    },
    [taskId],
  );

  // One load when the tab opens, then every 2 seconds while a run is in progress. The hook stops when
  // the tab unmounts.
  usePolling(load, 2000, [load], !unavailable && (data ? data.running : error === null));

  const reload = React.useCallback(() => {
    changes.current++;
    void load();
  }, [load]);

  if (unavailable) return <EmptyState icon={ShieldCheckIcon} title={swt("authz.notAvailable")} />;
  if (!data) {
    if (error === null) return null;
    return (
      <Card className="py-0">
        <ErrorState title={swt("authz.loadFailed")} detail={error} onRetry={reload} />
      </Card>
    );
  }

  const text = draft ?? data.policy;
  const dirty = text !== data.policy;
  const tooLarge = dirty && policyTooLarge(text);

  // An older server sends no document; the policy is then edited as text only.
  const matrixOffered = data.document !== undefined;
  const savedDoc = data.document ?? (data.policy.trim() === "" ? NO_POLICY : null);
  // The text was changed by hand since it was saved or last written from the matrix.
  const typed = dirty && draftDoc === null;
  const matrixDoc = typed ? null : (draftDoc ?? savedDoc);
  const view = matrixOffered && matrixDoc && picked !== "text" ? "matrix" : "text";

  function editMatrix(next: AuthzDocument) {
    setDraftDoc(next);
    setDraft(policyText(next));
  }

  function editText(next: string) {
    setDraft(next);
    setDraftDoc(null);
  }

  function discard() {
    setDraft(null);
    setDraftDoc(null);
  }

  async function savePolicy() {
    const sentDoc = draftDoc;
    setSaving(true);
    try {
      const saved = await api.saveAuthzPolicy(taskId, text);
      changes.current++;
      // The server's reading of the text; a server that sends none wrote what the matrix held.
      const document = saved.document !== undefined ? (saved.document ?? sentDoc) : undefined;
      setData(
        (current) =>
          current && {
            ...current,
            policy: text,
            policy_updated_at: saved.policy_updated_at,
            check: saved.check,
            document,
          },
      );
      // Keep what was changed while the request was on its way.
      if (latest.current.draft === text) discard();
      toast.success(swt("authz.policySaved"));
    } catch (e) {
      toast.error(swt("authz.policySaveFailed", { p0: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  }

  async function startRun() {
    setStarting(true);
    try {
      await api.startAuthzRun(taskId);
      changes.current++;
      setData((current) => current && { ...current, running: true });
      toast.success(swt("authz.runStarted"));
    } catch (e) {
      toast.error(swt("authz.runFailed", { p0: (e as Error).message }));
      // Another run may be in progress, or the saved policy may no longer validate.
      reload();
    } finally {
      setStarting(false);
    }
  }

  function insertExample() {
    editText(
      starterPolicy(data?.accounts ?? [], {
        intro: swt("authz.starterIntro"),
        baseUrl: swt("authz.starterBaseUrl"),
        signIn: swt("authz.starterSignIn"),
        token: swt("authz.starterToken"),
        fixedToken: swt("authz.starterFixedToken"),
        identity: swt("authz.starterIdentity"),
        cases: swt("authz.starterCases"),
        expect: swt("authz.starterExpect"),
      }),
    );
  }

  let blockedReason: string | null = null;
  if (dirty) blockedReason = swt("authz.runNeedsSave");
  else if (!data.check) blockedReason = swt("authz.runNeedsPolicy");
  else if (data.check.ok !== true) blockedReason = swt("authz.runNeedsValid");

  let policyStatus = swt("authz.stepPolicyProblems");
  if (dirty) policyStatus = swt("authz.stepPolicyUnsaved");
  else if (!data.check) policyStatus = swt("authz.stepPolicyNone");
  else if (data.check.ok) policyStatus = swt("authz.stepPolicyValid");

  const lastRun = data.runs[0];
  const resultLabels: Record<string, string> = {
    pass: swt("authz.resultPass"),
    fail: swt("authz.resultFail"),
    inconclusive: swt("authz.resultInconclusive"),
    error: swt("authz.resultError"),
  };
  let runStatus = swt("authz.stepRunNone");
  if (data.running) runStatus = swt("authz.stepRunRunning");
  else if (lastRun) runStatus = swt("authz.stepRunLast", { p0: resultLabels[lastRun.result] ?? lastRun.result });

  const views = [
    { value: "matrix" as const, label: swt("authz.viewMatrix") },
    { value: "text" as const, label: swt("authz.viewText") },
  ];

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <p className="text-muted-foreground text-sm">{swt("authz.intro")}</p>

      <Steps
        label={swt("authz.stepsLabel")}
        items={[
          {
            target: "authz-accounts",
            title: swt("authz.stepAccounts"),
            status:
              data.accounts.length > 0
                ? swt("authz.stepAccountsCount", { p0: data.accounts.length })
                : swt("authz.stepAccountsNone"),
            done: data.accounts.length > 0,
          },
          {
            target: "authz-policy",
            title: swt("authz.stepPolicy"),
            status: policyStatus,
            done: !dirty && data.check?.ok === true,
          },
          {
            target: "authz-results",
            title: swt("authz.stepRun"),
            status: runStatus,
            done: !data.running && lastRun?.result === "pass",
            failed: !data.running && lastRun?.result === "fail",
          },
        ]}
      />

      <div id="authz-accounts" className="min-w-0 scroll-mt-4">
        <AccountsCard taskId={taskId} accounts={data.accounts} onChanged={reload} />
      </div>

      <Card id="authz-policy" className="min-w-0 scroll-mt-4">
        <CardHeader>
          <CardTitle className="text-base">{swt("authz.policyTitle")}</CardTitle>
          <CardDescription>
            {swt("authz.policyDescription")}{" "}
            <Link href="/docs/#authorization-checks" className="font-medium text-primary hover:underline">
              {swt("authz.docsLink")}
            </Link>
          </CardDescription>
        </CardHeader>
        <CardContent className="flex min-w-0 flex-col gap-3">
          {matrixOffered && (
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
              {/* biome-ignore lint/a11y/useSemanticElements: two toggle buttons, not a form field set */}
              <div
                role="group"
                aria-label={swt("authz.viewLabel")}
                className="inline-flex shrink-0 overflow-hidden rounded-lg border"
              >
                {views.map((item, index) => (
                  <button
                    key={item.value}
                    type="button"
                    aria-pressed={view === item.value}
                    disabled={item.value === "matrix" && !matrixDoc}
                    className={cn(
                      "px-3 py-1.5 font-medium text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset disabled:opacity-50",
                      index > 0 && "border-l",
                      view === item.value ? "bg-primary text-primary-foreground" : "hover:bg-muted",
                    )}
                    onClick={() => setPicked(item.value)}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
              {!matrixDoc && (
                <span className="min-w-0 text-muted-foreground text-xs">
                  {typed ? swt("authz.matrixNeedsSave") : swt("authz.matrixNeedsFix")}
                </span>
              )}
            </div>
          )}
          {view === "matrix" && matrixDoc ? (
            <>
              {/^\s*#/m.test(data.policy) && (
                <p className="flex items-start gap-2 text-muted-foreground text-xs">
                  <InfoIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                  {swt("authz.matrixCommentsNote")}
                </p>
              )}
              <MatrixEditor doc={matrixDoc} accounts={data.accounts} disabled={saving} onChange={editMatrix} />
            </>
          ) : (
            <Textarea
              aria-label={swt("authz.policyLabel")}
              value={text}
              wrap="off"
              spellCheck={false}
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              placeholder={swt("authz.policyPlaceholder")}
              className="max-h-[60vh] min-h-64 font-mono text-xs md:text-xs"
              onChange={(event) => editText(event.target.value)}
            />
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" size="sm" disabled={!dirty || saving || tooLarge} onClick={() => void savePolicy()}>
              {saving && <Spinner data-icon="inline-start" />}
              {saving ? swt("authz.savingPolicy") : swt("authz.savePolicy")}
            </Button>
            {dirty && (
              <Button type="button" size="sm" variant="outline" disabled={saving} onClick={discard}>
                {swt("authz.discardChanges")}
              </Button>
            )}
            {view === "text" && text.trim() === "" && (
              <Button type="button" size="sm" variant="outline" onClick={insertExample}>
                {swt("authz.insertExample")}
              </Button>
            )}
            {dirty ? (
              <span className="font-medium text-amber-700 text-xs dark:text-amber-400">{swt("authz.unsaved")}</span>
            ) : (
              data.policy_updated_at && (
                <span className="text-muted-foreground text-xs">
                  {swt("authz.lastSaved", { p0: fmt.dateTime(data.policy_updated_at) })}
                </span>
              )
            )}
          </div>
          {tooLarge && (
            <p role="alert" className="text-destructive text-sm">
              {swt("authz.policyTooLarge")}
            </p>
          )}
          <PolicyCheck check={data.check} stale={dirty} />
        </CardContent>
      </Card>

      <div id="authz-results" className="min-w-0 scroll-mt-4">
        <AuthzResults
          taskId={taskId}
          runs={data.runs}
          running={data.running}
          starting={starting}
          blockedReason={blockedReason}
          onRun={() => void startRun()}
        />
      </div>
    </div>
  );
}
