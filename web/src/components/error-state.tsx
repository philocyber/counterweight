"use client";

import type { ReactNode } from "react";

import { RotateCcwIcon, TriangleAlertIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";
import { cn } from "@/lib/utils";

// ErrorState is the "this could not be loaded" counterpart of EmptyState: same footprint and type scale,
// so a failed request never reads as an empty result.
export function ErrorState({
  title,
  detail,
  onRetry,
  actions,
  className,
}: {
  title: ReactNode;
  detail?: ReactNode;
  onRetry?: () => void;
  actions?: ReactNode;
  className?: string;
}) {
  "use no memo";
  const { t: swt } = useI18n();

  return (
    <div
      role="alert"
      className={cn("flex flex-col items-center justify-center gap-1 px-4 py-8 text-center", className)}
    >
      <TriangleAlertIcon className="mb-1 size-5 text-destructive/80" aria-hidden="true" />
      <p className="font-medium text-sm">{title}</p>
      {detail && <p className="line-clamp-3 max-w-sm break-words text-muted-foreground text-xs">{detail}</p>}
      {(onRetry ?? actions) && (
        <div className="mt-3 flex flex-wrap items-center justify-center gap-2">
          {onRetry && (
            <Button size="sm" variant="outline" onClick={onRetry}>
              <RotateCcwIcon /> {swt("states.tryAgain")}
            </Button>
          )}
          {actions}
        </div>
      )}
    </div>
  );
}
