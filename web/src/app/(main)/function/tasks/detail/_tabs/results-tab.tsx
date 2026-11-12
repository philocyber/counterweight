"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowLeftIcon, ArrowRightIcon, RefreshCwIcon, SearchIcon, XIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { type TFunction, useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { nodeSummary, taskWorkspaceHref } from "@/lib/task-workspace";
import type { ExplorationNodePage, ExploreKind, TaskNode } from "@/lib/types";
import { cn } from "@/lib/utils";

function recordKind(node: TaskNode, swt: TFunction) {
  if (node.type === "finding") return swt("report.resultsKindFinding");
  if (node.type === "fact")
    return node.state === "origin" ? swt("report.resultsKindStart") : swt("report.resultsKindConclusion");
  return swt("report.resultsKindAttempt");
}

export function ResultsTab({ taskId }: { taskId: string }) {
  const { t: swt } = useI18n();
  const [query, setQuery] = React.useState("");
  const [search, setSearch] = React.useState("");
  const [kind, setKind] = React.useState("all");
  const [page, setPage] = React.useState(1);
  const [data, setData] = React.useState<ExplorationNodePage | null>(null);
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState(false);
  const [revision, refresh] = React.useReducer((n: number) => n + 1, 0);
  const size = 25;

  React.useEffect(() => {
    // An unchanged initial query must not clear a row selected during mount.
    if (query.trim() === search) return;
    const timer = setTimeout(() => {
      setSearch(query.trim());
      setPage(1);
      setSelectedId(null);
    }, 250);
    return () => clearTimeout(timer);
  }, [query, search]);

  React.useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    setLoading(true);
    setData(null);
    setError(false);
    async function load() {
      try {
        const result = await api.explorationNodes(taskId, {
          page,
          size,
          kinds: kind === "all" ? ["fact", "intent", "finding"] : [kind as ExploreKind],
          q: search,
          order: "desc",
        });
        if (!live) return;
        setData(result);
        setError(false);
        setSelectedId((current) => (result.items.some((node) => node.id === current) ? current : null));
      } catch {
        if (live) setError(true);
      } finally {
        if (live) {
          setLoading(false);
          timer = setTimeout(load, 15000);
        }
      }
    }
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [taskId, page, kind, search, revision]);

  const selected = data?.items.find((item) => item.id === selectedId);
  const assets = selected ? (data?.assets[selected.id] ?? []) : [];
  let payload = selected?.payload ?? "";
  try {
    payload = JSON.stringify(JSON.parse(payload), null, 2);
  } catch {
    /* Plain-text records remain readable. */
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">{swt("report.resultsTitle")}</h2>
          <p className="mt-1 max-w-3xl text-sm leading-relaxed text-muted-foreground">{swt("report.resultsIntro")}</p>
        </div>
        <Button variant="outline" size="sm" onClick={refresh}>
          <RefreshCwIcon /> {swt("common.refresh")}
        </Button>
      </div>
      <div className="rounded-md border bg-muted/30 p-3 text-xs leading-relaxed text-muted-foreground">
        {swt("report.resultsLedgerNote")}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-48 max-w-sm flex-1">
          <SearchIcon className="pointer-events-none absolute top-2.5 left-3 size-4 text-muted-foreground" />
          <Input
            aria-label={swt("report.resultsSearchAria")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={swt("report.resultsSearchPlaceholder")}
            className="pl-9"
          />
        </div>
        <Select
          value={kind}
          onValueChange={(value) => {
            setKind(value);
            setPage(1);
            setSelectedId(null);
          }}
        >
          <SelectTrigger aria-label={swt("report.resultsFilterAria")} className="w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{swt("report.resultsFilterAll")}</SelectItem>
            <SelectItem value="fact">{swt("report.resultsFilterFact")}</SelectItem>
            <SelectItem value="intent">{swt("report.resultsFilterIntent")}</SelectItem>
            <SelectItem value="finding">{swt("report.resultsFilterFinding")}</SelectItem>
          </SelectContent>
        </Select>
        <span className="ml-auto text-xs tabular-nums text-muted-foreground">
          {data ? swt("report.resultsRecordCount", { p0: data.total }) : ""}
        </span>
      </div>
      {error && (
        <div
          role="alert"
          className="flex items-center justify-between gap-3 rounded-md border border-destructive/30 p-3 text-sm"
        >
          <span>{data ? swt("report.resultsRefreshFailed") : swt("report.resultsLoadFailed")}</span>
          <Button size="sm" variant="outline" onClick={refresh}>
            {swt("common.retry")}
          </Button>
        </div>
      )}
      <div className={cn("grid min-w-0 gap-4", selected && "xl:grid-cols-[minmax(0,1fr)_20rem]")}>
        <div className="min-w-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{swt("report.resultsColRecord")}</TableHead>
                <TableHead>{swt("report.resultsColSummary")}</TableHead>
                <TableHead>{swt("report.resultsColState")}</TableHead>
                <TableHead className={cn(selected && "hidden 2xl:table-cell")}>
                  {swt("report.resultsColWhen")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {!data && loading && (
                <TableRow>
                  <TableCell colSpan={4} className="h-36 text-center text-muted-foreground" role="status">
                    {swt("report.resultsLoading")}
                  </TableCell>
                </TableRow>
              )}
              {data?.items.map((node) => (
                <TableRow key={node.id} data-state={selectedId === node.id ? "selected" : undefined}>
                  <TableCell className="align-top">
                    <Badge variant="outline" className="whitespace-nowrap">
                      {recordKind(node, swt)}
                    </Badge>
                    <code className="mt-1 block text-[11px] text-muted-foreground">#{node.id}</code>
                  </TableCell>
                  <TableCell className="max-w-lg whitespace-normal align-top">
                    <button
                      type="button"
                      className="w-full rounded text-left text-sm leading-relaxed underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-ring"
                      aria-expanded={selectedId === node.id}
                      aria-controls="result-inspector"
                      onClick={() => setSelectedId((current) => (current === node.id ? null : node.id))}
                    >
                      <span className="line-clamp-3 break-words">{nodeSummary(node.payload)}</span>
                    </button>
                    {node.inherited && (
                      <span className="mt-1 block text-xs text-muted-foreground">
                        {swt("report.resultsInheritedSource", { p0: node.source_task_id })}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="align-top">
                    <code className="rounded bg-muted px-1.5 py-1 text-[11px]">
                      {node.state || swt("report.resultsStateUnspecified")}
                    </code>
                    {node.type === "intent" && ["blocked", "exhausted", "stopped", "deleted"].includes(node.state) && (
                      <span className="mt-2 block text-xs text-muted-foreground">
                        {swt("report.resultsIncomplete")}
                      </span>
                    )}
                  </TableCell>
                  <TableCell
                    className={cn(
                      "whitespace-nowrap align-top text-xs text-muted-foreground",
                      selected && "hidden 2xl:table-cell",
                    )}
                  >
                    {node.ts ? new Date(node.ts).toLocaleString() : swt("report.resultsNotRecorded")}
                  </TableCell>
                </TableRow>
              ))}
              {data?.items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} className="h-36 text-center text-muted-foreground">
                    {search || kind !== "all" ? swt("report.resultsNoMatch") : swt("report.resultsEmpty")}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          <div className="mt-3 flex items-center justify-between text-xs text-muted-foreground">
            <span>
              {data
                ? swt("report.resultsPageOfRefresh", { p0: page, p1: Math.max(1, Math.ceil(data.total / size)) })
                : swt("report.resultsPageRefresh", { p0: page })}
            </span>
            <div className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={page === 1 || loading}
                onClick={() => {
                  setPage((n) => n - 1);
                  setSelectedId(null);
                }}
              >
                <ArrowLeftIcon /> {swt("common.previous")}
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={!data || page * size >= data.total || loading}
                onClick={() => {
                  setPage((n) => n + 1);
                  setSelectedId(null);
                }}
              >
                {swt("common.next")} <ArrowRightIcon />
              </Button>
            </div>
          </div>
        </div>
        {selected && (
          <aside
            id="result-inspector"
            aria-label={swt("report.resultsInspectorAria")}
            className="sticky top-0 max-h-[calc(100dvh-13rem-var(--header-height))] min-w-0 self-start overflow-y-auto rounded-lg border bg-card"
          >
            <div className="flex items-center justify-between border-b p-3">
              <h3 className="text-sm font-semibold">{swt("report.resultsRecordNumber", { p0: selected.id })}</h3>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={swt("report.resultsCloseDetails")}
                onClick={() => setSelectedId(null)}
              >
                <XIcon />
              </Button>
            </div>
            <div className="space-y-4 p-4">
              <p className="whitespace-pre-wrap break-words text-sm leading-relaxed">{nodeSummary(selected.payload)}</p>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-xs">
                <dt className="text-muted-foreground">{swt("report.resultsFieldType")}</dt>
                <dd>{recordKind(selected, swt)}</dd>
                <dt className="text-muted-foreground">{swt("report.resultsFieldState")}</dt>
                <dd>{selected.state}</dd>
                <dt className="text-muted-foreground">{swt("report.resultsFieldOrigin")}</dt>
                <dd className="break-all">{selected.origin || swt("report.resultsNotRecorded")}</dd>
                <dt className="text-muted-foreground">{swt("report.resultsFieldRecordedAt")}</dt>
                <dd>{selected.ts ? new Date(selected.ts).toLocaleString() : swt("report.resultsNotRecorded")}</dd>
              </dl>
              {selected.delete_reason && (
                <p className="text-xs">{swt("report.resultsRemovalReason", { p0: selected.delete_reason })}</p>
              )}
              {assets.length > 0 && (
                <div>
                  <h4 className="mb-2 text-xs font-semibold">{swt("report.resultsLinkedAssets")}</h4>
                  {assets.map((asset) => (
                    <p key={asset.id} className="break-all font-mono text-xs">
                      {asset.label}
                    </p>
                  ))}
                </div>
              )}
              <details>
                <summary className="cursor-pointer text-xs font-medium">{swt("report.resultsOriginalPayload")}</summary>
                <pre className="mt-2 max-h-96 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted p-3 text-xs">
                  {payload || swt("report.resultsNoPayload")}
                </pre>
              </details>
              <div className="flex flex-wrap gap-3 text-xs">
                <Link
                  className="text-primary underline underline-offset-4"
                  href={taskWorkspaceHref(taskId, "sessions")}
                >
                  {swt("report.resultsOpenSession")}
                </Link>
                <Link className="text-primary underline underline-offset-4" href={taskWorkspaceHref(taskId, "graph")}>
                  {swt("report.resultsOpenPaths")}
                </Link>
              </div>
            </div>
          </aside>
        )}
      </div>
    </div>
  );
}
