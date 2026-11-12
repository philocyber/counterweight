"use client";
import * as React from "react";

import ReactMarkdown, { type Components } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkGfm from "remark-gfm";

import { useI18n } from "@/i18n";

// Tailwind-styled element overrides (no typography plugin in this project, so we
// style each element). `node` is stripped — it's not a valid DOM attribute.
const components: Components = {
  h1: ({ node, ...p }) => <div className="mt-2.5 text-base font-semibold" {...p} />,
  h2: ({ node, ...p }) => <div className="mt-2 text-sm font-semibold" {...p} />,
  h3: ({ node, ...p }) => <div className="mt-1.5 text-sm font-medium text-foreground/90" {...p} />,
  h4: ({ node, ...p }) => <div className="mt-1.5 text-sm font-medium text-foreground/90" {...p} />,
  p: ({ node, ...p }) => <p className="leading-relaxed" {...p} />,
  ul: ({ node, ...p }) => <ul className="my-1 list-disc space-y-0.5 pl-5" {...p} />,
  ol: ({ node, ...p }) => <ol className="my-1 list-decimal space-y-0.5 pl-5" {...p} />,
  strong: ({ node, ...p }) => <strong className="font-semibold" {...p} />,
  a: ({ node, ...p }) => (
    <a className="text-primary underline underline-offset-2" target="_blank" rel="noreferrer" {...p} />
  ),
  hr: () => <hr className="my-2 border-border" />,
  // Text written by an agent must never make the browser fetch a URL by itself, so an image is shown
  // as its Markdown source and nothing is loaded.
  img: ({ node, src, alt }) => (
    <code className="break-all rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">{`![${alt ?? ""}](${typeof src === "string" ? src : ""})`}</code>
  ),
  blockquote: ({ node, ...p }) => (
    <blockquote className="my-1 border-l-2 border-border pl-2.5 text-muted-foreground" {...p} />
  ),
  pre: ({ node, ...p }) => (
    <pre className="my-1.5 overflow-auto rounded-md bg-muted/70 p-2.5 text-xs leading-relaxed" {...p} />
  ),
  code: ({ node, className, children, ...rest }) => {
    const block = /language-/.test(className || "");
    return block ? (
      <code className={"font-mono text-xs " + (className || "")} {...rest}>
        {children}
      </code>
    ) : (
      <code className="break-all rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]" {...rest}>
        {children}
      </code>
    );
  },
  table: ({ node, ...p }) => (
    <div className="my-1.5 overflow-x-auto">
      <table className="w-full border-collapse text-xs" {...p} />
    </div>
  ),
  th: ({ node, ...p }) => <th className="border border-border bg-muted/60 px-2 py-1 text-left font-medium" {...p} />,
  td: ({ node, ...p }) => <td className="border border-border px-2 py-1 align-top" {...p} />,
};

// Long-form overrides for reports: readable measure, clear heading hierarchy and
// roomier code and tables. Chat keeps the compact set above.
const documentComponents: Components = {
  ...components,
  h1: ({ node, ...p }) => <h3 className="mt-6 text-lg font-semibold tracking-tight" {...p} />,
  h2: ({ node, ...p }) => <h4 className="mt-6 border-b pb-1.5 text-base font-semibold tracking-tight" {...p} />,
  h3: ({ node, ...p }) => <h5 className="mt-5 text-sm font-semibold" {...p} />,
  h4: ({ node, ...p }) => <h6 className="mt-4 text-sm font-semibold text-foreground/85" {...p} />,
  p: ({ node, ...p }) => <p className="leading-7 text-foreground/90" {...p} />,
  ul: ({ node, ...p }) => <ul className="my-2 list-disc space-y-1.5 pl-5 leading-7 marker:text-primary" {...p} />,
  ol: ({ node, ...p }) => (
    <ol className="my-2 list-decimal space-y-1.5 pl-5 leading-7 marker:text-muted-foreground" {...p} />
  ),
  blockquote: ({ node, ...p }) => (
    <blockquote
      className="my-3 rounded-r-md border-l-2 border-primary bg-primary/5 px-4 py-2 text-foreground/85"
      {...p}
    />
  ),
  pre: ({ node, ...p }) => (
    <pre className="my-3 overflow-auto rounded-lg border bg-muted/60 p-4 text-[12.5px] leading-relaxed" {...p} />
  ),
  table: ({ node, ...p }) => (
    <div className="my-3 overflow-x-auto rounded-lg border">
      <table className="w-full border-collapse text-sm" {...p} />
    </div>
  ),
  th: ({ node, ...p }) => (
    <th
      className="border-b bg-muted/60 px-3 py-2 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground"
      {...p}
    />
  ),
  td: ({ node, ...p }) => <td className="border-b px-3 py-2 align-top last:border-b-0" {...p} />,
  hr: () => <hr className="my-5 border-border" />,
};

export function Markdown({ text, variant = "compact" }: { text: string; variant?: "compact" | "document" }) {
  "use no memo";
  const { locale: swLocale } = useI18n();
  const doc = variant === "document";

  return (
    <div
      className={
        doc ? "min-w-0 space-y-3 text-sm [&>*:first-child]:mt-0" : "min-w-0 space-y-1.5 text-sm [&>*:first-child]:mt-0"
      }
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        // Documents (reports) highlight fenced code; detection is off so plain
        // text and command output are not mis-coloured.
        rehypePlugins={doc ? [[rehypeHighlight, { detect: false }]] : []}
        components={doc ? documentComponents : components}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}
