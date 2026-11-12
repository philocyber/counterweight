import { cn } from "@/lib/utils";

// Label plus a quiet count, for tab rows. The count picks up the active tab.
export function CountLabel({ label, count, className }: { label: string; count?: number; className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      {label}
      {typeof count === "number" && (
        <span className="rounded-full bg-muted px-1.5 py-px font-medium text-[11px] text-muted-foreground tabular-nums in-data-active:bg-accent in-data-active:text-accent-foreground">
          {count}
        </span>
      )}
    </span>
  );
}
