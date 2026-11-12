import { cn } from "@/lib/utils";

// "+N" pill for values that do not fit in a row.
export function OverflowChip({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center rounded-full bg-muted px-1.5 font-medium text-[11px] text-muted-foreground tabular-nums",
        className,
      )}
    >
      +{count}
    </span>
  );
}
