"use client";

import * as React from "react";

import { cn } from "@/lib/utils";

interface ExpandableTextProps {
  children: React.ReactNode;
  lines?: number;
  className?: string;
}

// Clamps long copy to a few lines and offers a toggle only when the text actually overflows.
// The full text stays in the DOM, so assistive technology still reads all of it.
export function ExpandableText({ children, lines = 3, className }: ExpandableTextProps) {
  const ref = React.useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = React.useState(false);
  const [overflows, setOverflows] = React.useState(false);

  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el || expanded) return;
    const measure = () => setOverflows(el.scrollHeight > el.clientHeight + 1);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [expanded]);

  return (
    <div className={className}>
      <div
        ref={ref}
        className={cn(!expanded && "overflow-hidden")}
        style={expanded ? undefined : { display: "-webkit-box", WebkitLineClamp: lines, WebkitBoxOrient: "vertical" }}
      >
        {children}
      </div>
      {(overflows || expanded) && (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-primary text-xs underline-offset-4 hover:underline"
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      )}
    </div>
  );
}
