"use client";

import * as React from "react";

import { CheckIcon, CopyIcon, DownloadIcon, FileTextIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { ReportView, TaskExecution } from "@/lib/types";
import { copyText } from "@/lib/utils";

import type { JiraState } from "./jira-export";
import { ReportDocument } from "./report-view";

export function ReportTab({
  taskId,
  jira,
  execution,
  status,
}: {
  taskId: string;
  jira: JiraState;
  execution?: TaskExecution;
  status?: string;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [report, setReport] = React.useState<string>("");
  const [view, setView] = React.useState<ReportView | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const [error, setError] = React.useState(false);
  const [pdfLoading, setPdfLoading] = React.useState(false);
  const [exportOpen, setExportOpen] = React.useState(false);
  const [variant, setVariant] = React.useState<"technical" | "executive">("technical");
  const [includeModels, setIncludeModels] = React.useState(false);
  const [includeSecrets, setIncludeSecrets] = React.useState(false);
  const [revision, refresh] = React.useReducer((n: number) => n + 1, 0);

  React.useEffect(() => {
    let active = true;
    setLoading(true);
    setReport("");
    setView(null);
    setError(false);
    // The structured view drives the layout; Markdown backs download/copy and is
    // the fallback when the backend predates the structured format.
    Promise.allSettled([api.report(taskId), api.reportView(taskId)])
      .then(([markdown, structured]) => {
        if (!active) return;
        if (markdown.status === "fulfilled") setReport(markdown.value);
        if (structured.status === "fulfilled") setView(structured.value);
        if (markdown.status === "rejected" && structured.status === "rejected") setError(true);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [taskId, revision]);

  function save(blob: Blob, extension: string, name = "report") {
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `counterweight-task-${taskId.replace(/[^a-zA-Z0-9_-]/g, "_")}-${name}.${extension}`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function download() {
    save(new Blob([report], { type: "text/markdown;charset=utf-8" }), "md");
  }

  async function downloadPdf() {
    setPdfLoading(true);
    try {
      const blob = await api.reportPdf(taskId, { variant, models: includeModels, secrets: includeSecrets });
      save(blob, "pdf", variant === "executive" ? "executive-summary" : "report");
      setExportOpen(false);
    } catch {
      toast.error(swt("report.tabPdfError"));
    } finally {
      setPdfLoading(false);
    }
  }

  async function downloadSarif() {
    try {
      save(await api.reportSarif(taskId), "sarif", "report");
    } catch {
      toast.error(swt("report.tabSarifError"));
    }
  }

  async function copy() {
    if (!report) return;
    const ok = await copyText(report);
    if (ok) {
      setCopied(true);
      toast.success(swt("interface.m0676"));
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error(swt("interface.m0677"));
    }
  }

  let content: React.ReactNode;
  if (loading) {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        {swt("interface.m0260")}
      </div>
    );
  } else if (error) {
    content = (
      <div
        role="alert"
        className="flex items-center justify-between gap-3 rounded-md border border-destructive/30 p-4 text-sm"
      >
        <span>{swt("report.tabLoadError")}</span>
        <Button variant="outline" size="sm" onClick={refresh}>
          {swt("common.retry")}
        </Button>
      </div>
    );
  } else if (view) {
    content = (
      <div className="rounded-xl bg-muted/40 p-3 sm:p-5 lg:p-6">
        <ReportDocument
          view={view}
          jira={{ links: jira.links, exportFinding: jira.exportFinding }}
          execution={execution}
          status={status}
        />
      </div>
    );
  } else if (report) {
    content = (
      <div className="overflow-x-auto rounded-md border bg-card p-5 lg:p-8">
        <Markdown text={report} variant="document" />
      </div>
    );
  } else {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        {swt("interface.m0678")}
      </div>
    );
  }

  return (
    <Card>
      <CardHeader className="sticky top-0 z-10 flex-row flex-wrap items-center justify-between gap-3 border-b bg-card">
        <CardTitle className="flex items-center gap-2 text-sm">
          <FileTextIcon className="size-4" />{" "}
          {view?.kind === "code_review"
            ? swt("report.tabTitleCodeReview")
            : view
              ? swt("report.tabTitlePentest")
              : swt("interface.m0679")}
        </CardTitle>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={refresh} disabled={loading}>
            <RefreshCwIcon /> {swt("common.refresh")}
          </Button>
          {report && (
            <Button size="sm" onClick={() => setExportOpen(true)} disabled={pdfLoading}>
              <DownloadIcon /> {pdfLoading ? swt("report.tabGeneratingPdf") : swt("report.tabExportPdf")}
            </Button>
          )}
          {report && (
            <Button size="sm" variant="outline" onClick={download}>
              <DownloadIcon /> {swt("report.tabDownloadMarkdown")}
            </Button>
          )}
          {report && (
            <Button size="sm" variant="outline" onClick={downloadSarif}>
              <DownloadIcon /> {swt("report.tabDownloadSarif")}
            </Button>
          )}
          {report && (
            <Button size="sm" variant="outline" onClick={copy}>
              {copied ? <CheckIcon /> : <CopyIcon />} {swt("interface.m0680")}
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent>
        <p className="mb-4 max-w-3xl text-xs leading-relaxed text-muted-foreground">{swt("report.tabDisclaimer")}</p>
        {report && (
          <p className="mb-4 max-w-3xl text-xs leading-relaxed text-muted-foreground">
            {swt("report.exportSarifHint")}
          </p>
        )}
        {content}
      </CardContent>
      <Dialog open={exportOpen} onOpenChange={setExportOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{swt("report.tabPdfDialogTitle")}</DialogTitle>
            <DialogDescription>{swt("report.tabPdfDialogDescription")}</DialogDescription>
          </DialogHeader>
          <RadioGroup
            aria-label={swt("report.tabVariantAria")}
            value={variant}
            onValueChange={(v) => setVariant(v as "technical" | "executive")}
            className="flex flex-col gap-2"
          >
            {[
              {
                value: "technical",
                title: swt("report.tabVariantTechnical"),
                hint: swt("report.tabVariantTechnicalHint"),
              },
              {
                value: "executive",
                title: swt("report.tabVariantExecutive"),
                hint: swt("report.tabVariantExecutiveHint"),
              },
            ].map((o) => (
              <Label
                key={o.value}
                htmlFor={`export-${o.value}`}
                className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 font-normal has-[[data-state=checked]]:border-primary/60 has-[[data-state=checked]]:bg-primary/5"
              >
                <RadioGroupItem id={`export-${o.value}`} value={o.value} className="mt-0.5" />
                <span className="flex flex-col gap-1">
                  <span className="font-medium text-sm">{o.title}</span>
                  <span className="text-muted-foreground text-xs leading-relaxed">{o.hint}</span>
                </span>
              </Label>
            ))}
          </RadioGroup>
          <div className="flex flex-col gap-3 border-t pt-4">
            <Label className="flex items-start gap-3 font-normal">
              <Checkbox
                checked={includeModels}
                onCheckedChange={(v) => setIncludeModels(v === true)}
                className="mt-0.5"
              />
              <span className="flex flex-col gap-0.5">
                <span className="text-sm">{swt("report.tabModelsTitle")}</span>
                <span className="text-muted-foreground text-xs">{swt("report.tabModelsHint")}</span>
              </span>
            </Label>
            <Label className="flex items-start gap-3 font-normal">
              <Checkbox
                checked={includeSecrets}
                onCheckedChange={(v) => setIncludeSecrets(v === true)}
                className="mt-0.5"
              />
              <span className="flex flex-col gap-0.5">
                <span className="text-sm">{swt("report.tabRedactionTitle")}</span>
                <span className="text-muted-foreground text-xs">{swt("report.tabRedactionHint")}</span>
              </span>
            </Label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setExportOpen(false)}>
              {swt("common.cancel")}
            </Button>
            <Button onClick={downloadPdf} disabled={pdfLoading}>
              <DownloadIcon /> {pdfLoading ? swt("report.tabGeneratingPdf") : swt("report.tabDownloadPdf")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
