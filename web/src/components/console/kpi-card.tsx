import type { ReactNode } from "react";

import type { LucideIcon } from "lucide-react";

import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// Compact metric: a quiet label, a large figure, and an optional breakdown.
export function KpiCard({
  icon: Icon,
  label,
  value,
  children,
  className,
}: {
  icon?: LucideIcon;
  label: ReactNode;
  value: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <Card className={cn("gap-1", className)}>
      <CardHeader className="pb-0">
        <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
          {Icon && <Icon className="size-3.5" />}
          {label}
        </div>
        <div className="font-semibold text-2xl tabular-nums tracking-tight">{value}</div>
      </CardHeader>
      {children && <CardContent className="text-[11px] text-muted-foreground">{children}</CardContent>}
    </Card>
  );
}
