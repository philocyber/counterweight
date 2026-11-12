"use client";

import * as React from "react";

import Link from "next/link";

import { AlertTriangleIcon, ArrowUpRightIcon, InfoIcon, SearchIcon } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { CopyButton } from "@/components/copy-button";
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

import { DOC_SECTIONS, type DocBlock, type DocSection } from "./_content";

function blockText(b: DocBlock): string {
  switch (b.type) {
    case "p":
    case "code":
    case "markdown":
      return b.text;
    case "list":
      return b.items.join(" ");
    case "steps":
      return b.items.map((i) => `${i.title} ${i.body}`).join(" ");
    case "table":
      return [...b.head, ...b.rows.flat()].join(" ");
    case "callout":
      return `${b.title} ${b.text}`;
    case "features":
      return b.items.map((i) => `${i.title} ${i.body}`).join(" ");
    case "faq":
      return b.items.map((i) => `${i.q} ${i.a}`).join(" ");
    case "figure":
      return `${b.alt} ${b.caption ?? ""}`;
  }
}

function sectionMatches(s: DocSection, q: string) {
  if (!q) return true;
  const hay = `${s.title} ${s.summary} ${s.blocks.map(blockText).join(" ")}`.toLowerCase();
  return q.split(/\s+/).every((w) => hay.includes(w));
}

function CodeSnippet({ text, language = "text" }: { text: string; language?: string }) {
  return (
    <div className="min-w-0 overflow-hidden rounded-lg border bg-muted/30">
      <div className="flex items-center justify-between gap-3 border-b bg-muted/50 px-4 py-2">
        <span className="font-mono text-muted-foreground text-xs">{language}</span>
        <CopyButton text={text} label="Copy code" className="h-7 text-xs" />
      </div>
      <pre className="overflow-x-auto p-4 font-mono text-xs leading-6">
        <code className={`language-${language}`}>{text}</code>
      </pre>
    </div>
  );
}

function GuideMarkdown({ text }: { text: string }) {
  return (
    <div className="min-w-0 space-y-5 text-foreground/90 text-sm leading-relaxed">
      <ReactMarkdown
        skipHtml
        remarkPlugins={[remarkGfm]}
        components={{
          h3: ({ children }) => {
            const title = String(children);
            let id: string | undefined;
            if (title.startsWith("6.")) id = "runpod-profile";
            if (title.startsWith("Troubleshooting")) id = "runpod-troubleshooting";
            return (
              <h3 id={id} className="scroll-mt-20 border-t pt-6 font-semibold text-base tracking-tight">
                {children}
              </h3>
            );
          },
          p: ({ children }) => <p className="leading-7">{children}</p>,
          a: ({ href, children }) => (
            <a href={href} className="text-primary underline underline-offset-4">
              {children}
            </a>
          ),
          ul: ({ children }) => <ul className="list-disc space-y-2 pl-5 marker:text-muted-foreground">{children}</ul>,
          ol: ({ children }) => (
            <ol className="list-decimal space-y-3 pl-5 marker:font-semibold marker:text-primary">{children}</ol>
          ),
          blockquote: ({ children }) => (
            <blockquote className="rounded-lg border border-amber-500/40 bg-amber-500/5 p-4">{children}</blockquote>
          ),
          table: ({ children }) => (
            <div className="overflow-x-auto rounded-lg border">
              <table className="w-full text-sm">{children}</table>
            </div>
          ),
          thead: ({ children }) => <thead className="bg-muted/50 text-left">{children}</thead>,
          th: ({ children }) => <th className="px-4 py-3 font-medium">{children}</th>,
          td: ({ children }) => <td className="border-t px-4 py-3 align-top">{children}</td>,
          pre: ({ children }) => {
            const child = React.Children.toArray(children)[0];
            if (React.isValidElement<{ children?: React.ReactNode; className?: string }>(child)) {
              return (
                <CodeSnippet
                  text={String(child.props.children ?? "").replace(/\n$/, "")}
                  language={child.props.className?.replace("language-", "")}
                />
              );
            }
            return <pre className="overflow-x-auto">{children}</pre>;
          },
          code: ({ children, className }) => (
            <code className={className ?? "break-words rounded bg-muted px-1.5 py-0.5 font-mono text-xs"}>
              {children}
            </code>
          ),
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

function Block({ block }: { block: DocBlock }) {
  switch (block.type) {
    case "p":
      return <p className="text-foreground/90 text-sm leading-relaxed">{block.text}</p>;
    case "code":
      return <CodeSnippet text={block.text} />;
    case "figure":
      return (
        <figure className="max-w-[64rem] space-y-2">
          {/* biome-ignore lint/performance/noImgElement: static export, plain SVG files */}
          <img src={block.light} alt={block.alt} width={1000} height={680} className="h-auto w-full dark:hidden" />
          {/* biome-ignore lint/performance/noImgElement: static export, plain SVG files */}
          <img src={block.dark} alt={block.alt} width={1000} height={680} className="hidden h-auto w-full dark:block" />
          {block.caption ? <figcaption className="text-muted-foreground text-xs">{block.caption}</figcaption> : null}
        </figure>
      );
    case "markdown":
      return <GuideMarkdown text={block.text} />;
    case "list":
      return (
        <ul className="list-disc space-y-1.5 pl-5 text-foreground/90 text-sm leading-relaxed marker:text-muted-foreground">
          {block.items.map((i) => (
            <li key={i}>{i}</li>
          ))}
        </ul>
      );
    case "steps":
      return (
        <ol className="space-y-3">
          {block.items.map((step, idx) => (
            <li key={step.title} className="flex gap-3">
              <span className="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-[11px] text-muted-foreground tabular-nums">
                {idx + 1}
              </span>
              <div className="min-w-0 text-sm leading-relaxed">
                <div className="flex flex-wrap items-center gap-2 font-medium">
                  {step.title}
                  {step.href && (
                    <Link
                      href={step.href}
                      prefetch={false}
                      className="inline-flex items-center gap-0.5 font-normal text-primary text-xs underline-offset-4 hover:underline"
                    >
                      Open <ArrowUpRightIcon className="size-3" />
                    </Link>
                  )}
                </div>
                <p className="text-foreground/80">{step.body}</p>
              </div>
            </li>
          ))}
        </ol>
      );
    case "table":
      return (
        <div className="overflow-x-auto rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                {block.head.map((h) => (
                  <TableHead key={h}>{h}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {block.rows.map((row) => (
                <TableRow key={row[0]}>
                  {row.map((cell, i) => (
                    <TableCell
                      key={`${row[0]}-${block.head[i]}`}
                      className={cn("whitespace-normal align-top", i === 0 && "font-medium")}
                    >
                      {cell}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      );
    case "callout": {
      const Icon = block.tone === "warn" ? AlertTriangleIcon : InfoIcon;
      return (
        <Alert className={cn(block.tone === "warn" && "border-amber-500/40 bg-amber-500/5")}>
          <Icon className={cn(block.tone === "warn" && "text-amber-700 dark:text-amber-400")} />
          <AlertTitle>{block.title}</AlertTitle>
          <AlertDescription>{block.text}</AlertDescription>
        </Alert>
      );
    }
    case "features":
      return (
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {block.items.map((f) => (
            <Link key={f.href} href={f.href} prefetch={false} className="group">
              <Card className="h-full transition-colors group-hover:border-primary/50">
                <CardContent className="space-y-1 p-4">
                  <div className="flex items-center justify-between font-medium text-sm">
                    {f.title}
                    <ArrowUpRightIcon className="size-3.5 text-muted-foreground transition-colors group-hover:text-primary" />
                  </div>
                  <p className="text-muted-foreground text-xs leading-relaxed">{f.body}</p>
                </CardContent>
              </Card>
            </Link>
          ))}
        </div>
      );
    case "faq":
      return (
        <Accordion type="multiple" className="rounded-md border px-4">
          {block.items.map((f) => (
            <AccordionItem key={f.q} value={f.q}>
              <AccordionTrigger className="text-left text-sm">{f.q}</AccordionTrigger>
              <AccordionContent className="text-foreground/80 text-sm leading-relaxed">{f.a}</AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>
      );
  }
}

export default function DocsPage() {
  const [query, setQuery] = React.useState("");
  const [active, setActive] = React.useState(DOC_SECTIONS[0].id);

  const q = query.trim().toLowerCase();
  const sections = React.useMemo(() => DOC_SECTIONS.filter((s) => sectionMatches(s, q)), [q]);

  // Scroll-spy: highlight the section nearest the top of the viewport.
  React.useEffect(() => {
    const els = sections.map((s) => document.getElementById(s.id)).filter((e): e is HTMLElement => !!e);
    if (!els.length) return;
    const io = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]) setActive(visible[0].target.id);
      },
      { rootMargin: "-10% 0px -75% 0px" },
    );
    for (const el of els) io.observe(el);
    return () => io.disconnect();
  }, [sections]);

  return (
    <div className="grid w-full grid-cols-[minmax(0,1fr)] gap-8 lg:grid-cols-[15rem_minmax(0,74rem)] xl:gap-10">
      <aside className="min-w-0 lg:sticky lg:top-[calc(var(--header-height)+1.5rem)] lg:max-h-[calc(100dvh-var(--header-height)-3rem)] lg:self-start lg:overflow-y-auto">
        <div className="relative mb-3">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search docs…"
            aria-label="Search docs"
            className="h-8 pl-8 text-sm"
          />
        </div>
        <nav aria-label="Documentation sections" className="flex gap-1 overflow-x-auto lg:flex-col lg:overflow-visible">
          {sections.map((s) => {
            const Icon = s.icon;
            return (
              <a
                key={s.id}
                href={`#${s.id}`}
                aria-current={active === s.id ? "true" : undefined}
                className={cn(
                  "flex shrink-0 items-center gap-2 rounded-md px-2.5 py-1.5 text-muted-foreground text-sm transition-colors hover:bg-accent hover:text-foreground",
                  active === s.id && "bg-accent font-medium text-foreground",
                )}
              >
                <Icon className="size-4" />
                {s.title}
              </a>
            );
          })}
        </nav>
      </aside>

      <div className="min-w-0 space-y-12">
        {sections.length === 0 && <p className="text-muted-foreground text-sm">No sections match “{query}”.</p>}
        {sections.map((s) => (
          <section key={s.id} id={s.id} className="scroll-mt-20 space-y-4">
            <header className="space-y-1">
              <h2 className="font-semibold text-xl tracking-tight">{s.title}</h2>
              <p className="text-muted-foreground text-sm">{s.summary}</p>
            </header>
            {s.blocks.map((b) => (
              <Block key={`${s.id}-${blockText(b).slice(0, 48)}`} block={b} />
            ))}
          </section>
        ))}
      </div>
    </div>
  );
}
