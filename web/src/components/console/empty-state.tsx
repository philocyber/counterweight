import type { ReactNode } from "react";

import type { LucideIcon } from "lucide-react";

import { cn } from "@/lib/utils";

export function EmptyState({
  icon: Icon,
  title,
  hint,
  className,
}: {
  icon?: LucideIcon;
  title: ReactNode;
  hint?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-1 px-4 py-8 text-center", className)}>
      {Icon && <Icon className="mb-1 size-5 text-muted-foreground/70" />}
      <p className="font-medium text-sm">{title}</p>
      {hint && <p className="max-w-sm text-muted-foreground text-xs">{hint}</p>}
    </div>
  );
}
