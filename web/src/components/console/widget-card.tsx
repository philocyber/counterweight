import type { ReactNode } from "react";

import type { LucideIcon } from "lucide-react";

import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// Titled surface used by dashboard widgets: heading and an optional action on one row.
export function WidgetCard({
  icon: Icon,
  title,
  action,
  children,
  className,
  bodyClassName,
}: {
  icon?: LucideIcon;
  title: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <Card className={cn("gap-0 p-4", className)}>
      <div className="mb-3 flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1.5 font-semibold text-sm">
          {Icon && <Icon className="size-3.5 shrink-0 text-muted-foreground" />}
          <span className="truncate">{title}</span>
        </div>
        {action && <div className="flex shrink-0 items-center gap-2 text-xs">{action}</div>}
      </div>
      <div className={cn("min-w-0", bodyClassName)}>{children}</div>
    </Card>
  );
}
