"use client";
import * as React from "react";

import Link from "next/link";

import { RotateCcwIcon, ScanSearchIcon, SquareIcon } from "lucide-react";
import { toast } from "sonner";

import { FindingRetestDialog } from "@/components/finding-retest-dialog";
import { Markdown } from "@/components/markdown";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { useI18n } from "@/i18n";
import { getIntlLocale, translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { FindingRetest } from "@/lib/types";

const statusLabels = {
  get pending() {
    return swt("interface.m2216");
  },
  get running() {
    return swt("interface.m0330");
  },
  get completed() {
    return swt("interface.m0809");
  },
  get failed() {
    return swt("interface.m2217");
  },
  get stopped() {
    return swt("interface.m2218");
  },
};
const verdictLabels = {
  get reproduced() {
    return swt("interface.m2219");
  },
  get fixed() {
    return swt("interface.m2220");
  },
  get inconclusive() {
    return swt("interface.m2221");
  },
  get confirmed() {
    return swt("interface.codeVerifyVerdictConfirmed");
  },
  get needs_validation() {
    return swt("interface.codeVerifyVerdictNeedsValidation");
  },
  get rejected() {
    return swt("interface.codeVerifyVerdictRejected");
  },
};

function active(r: FindingRetest) {
  return r.status === "pending" || r.status === "running";
}

export function FindingRetestPanel({
  findingId,
  findingName,
  readOnly = false,
  codeReview = false,
  onCompleted,
}: {
  findingId: string;
  findingName?: string;
  readOnly?: boolean;
  // A code review finding is re-checked by an independent verification of the source.
  codeReview?: boolean;
  onCompleted?: () => void;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [items, setItems] = React.useState<FindingRetest[] | null>(null);
  const [error, setError] = React.useState("");
  const [open, setOpen] = React.useState(false);
  const [stopping, setStopping] = React.useState<number | null>(null);
  const requestSeq = React.useRef(0);
  const previousItems = React.useRef<FindingRetest[]>([]);

  const load = React.useCallback(async () => {
    const seq = ++requestSeq.current;
    try {
      const rows = await api.findingRetests(findingId);
      if (seq !== requestSeq.current) return;
      const completed = previousItems.current.some(
        (item) => active(item) && rows.some((row) => row.id === item.id && !active(row)),
      );
      previousItems.current = rows;
      setItems(rows);
      setError("");
      if (completed) onCompleted?.();
    } catch (e) {
      if (seq === requestSeq.current) setError((e as Error).message);
    }
  }, [findingId, onCompleted]);

  React.useEffect(() => {
    void load();
    return () => {
      requestSeq.current++;
    };
  }, [load]);

  React.useEffect(() => {
    if (error || !items?.some(active)) return;
    const timer = setTimeout(() => void load(), 3000);
    return () => clearTimeout(timer);
  }, [error, items, load]);

  const running = items?.find(active);

  const [verifying, setVerifying] = React.useState(false);
  const verify = async () => {
    setVerifying(true);
    try {
      await api.verifyFinding(findingId);
      toast.success(swt("interface.verifyFindingStarted"));
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setVerifying(false);
    }
  };

  const stop = async (runId: number) => {
    setStopping(runId);
    try {
      await api.stopFindingRun(runId);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setStopping(null);
    }
  };

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <CardTitle>{swt("interface.m2222")}</CardTitle>
          <CardDescription>{swt("interface.m2223")}</CardDescription>
        </div>
        {running ? (
          <Button variant="outline" size="sm" disabled title={swt("interface.m0329")}>
            <Spinner data-icon="inline-start" aria-hidden="true" />
            {swt("interface.m0330")}
          </Button>
        ) : null}
        {!running && !readOnly && codeReview ? (
          <Button size="sm" onClick={() => void verify()} disabled={items === null || !!error || verifying}>
            <ScanSearchIcon data-icon="inline-start" />
            {swt("interface.verifyFindingStart")}
          </Button>
        ) : null}
        {!running && !readOnly && !codeReview ? (
          <Button size="sm" onClick={() => setOpen(true)} disabled={items === null || !!error}>
            <RotateCcwIcon data-icon="inline-start" />
            {swt("interface.m2224")}
          </Button>
        ) : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>
              {swt("interface.m2225")}
              {error}
              <Button variant="outline" size="sm" onClick={() => void load()}>
                {swt("interface.m0682")}
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
        {!error && items === null ? <Skeleton className="h-16 w-full" /> : null}
        {!error && items?.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>{swt("interface.m2226")}</EmptyTitle>
              <EmptyDescription>{swt("interface.m2227")}</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : null}
        {!error && items
          ? items.map((item) => (
              <div key={item.id} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge
                    variant={
                      item.status === "completed" && (item.verdict === "reproduced" || item.verdict === "confirmed")
                        ? "destructive"
                        : "secondary"
                    }
                  >
                    {item.status === "completed" && item.verdict in verdictLabels
                      ? verdictLabels[item.verdict as keyof typeof verdictLabels]
                      : statusLabels[item.status]}
                  </Badge>
                  {item.kind === "verify" ? <Badge variant="outline">{swt("interface.codeVerifyKind")}</Badge> : null}
                  <span className="text-muted-foreground text-xs">
                    #{item.id} · {new Date(item.created_at).toLocaleString(getIntlLocale())}
                  </span>
                  {active(item) && !readOnly ? (
                    <Button
                      variant="outline"
                      size="sm"
                      className="ml-auto"
                      title={swt("interface.findingRunStopTitle")}
                      disabled={stopping === item.id}
                      onClick={() => void stop(item.id)}
                    >
                      {stopping === item.id ? (
                        <Spinner data-icon="inline-start" aria-hidden="true" />
                      ) : (
                        <SquareIcon data-icon="inline-start" />
                      )}
                      {swt("interface.findingRunStop")}
                    </Button>
                  ) : null}
                  {item.conversation_id != null ? (
                    <Button
                      asChild
                      variant="ghost"
                      size="sm"
                      className={active(item) && !readOnly ? undefined : "ml-auto"}
                    >
                      <Link href={`/chat?c=${item.conversation_id}`}>{swt("interface.m2228")}</Link>
                    </Button>
                  ) : (
                    <span className="text-muted-foreground text-xs">{swt("interface.m2229")}</span>
                  )}
                </div>
                {item.status === "completed" && item.summary ? (
                  <p className="whitespace-pre-wrap break-words text-sm">{item.summary}</p>
                ) : null}
                {item.error ? (
                  <p className="whitespace-pre-wrap break-words text-destructive text-sm">{item.error}</p>
                ) : null}
                {item.notes ? (
                  <p className="whitespace-pre-wrap break-words text-muted-foreground text-xs">
                    {swt("interface.m2230")}
                    {item.notes}
                  </p>
                ) : null}
                {item.status === "completed" && item.evidence ? (
                  <details className="min-w-0">
                    <summary className="cursor-pointer text-sm">{swt("interface.m2231")}</summary>
                    <div className="mt-3 overflow-x-auto">
                      <Markdown text={item.evidence} />
                    </div>
                  </details>
                ) : null}
              </div>
            ))
          : null}
      </CardContent>
      {open ? (
        <FindingRetestDialog
          findingId={findingId}
          findingName={findingName}
          onClose={() => setOpen(false)}
          onStarted={(retest) => {
            requestSeq.current++;
            const rows = [retest, ...previousItems.current.filter((item) => item.id !== retest.id)];
            previousItems.current = rows;
            setItems(rows);
            setError("");
            void load();
          }}
        />
      ) : null}
    </Card>
  );
}
