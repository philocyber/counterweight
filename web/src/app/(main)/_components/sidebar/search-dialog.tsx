"use client";
import * as React from "react";

import { useRouter } from "next/navigation";

import { Search } from "lucide-react";

import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { NavMainItem } from "@/navigation/sidebar/sidebar-items";
import { sidebarItems } from "@/navigation/sidebar/sidebar-items";

type SearchItem = {
  id: string;
  group: string;
  label: string;
  // Hidden search terms (previous names of the page).
  keywords?: string;
  url: string;
  icon?: NavMainItem["icon"];
  disabled?: boolean;
  newTab?: boolean;
};

type AttentionItem = {
  href: string;
  dot: string;
  title: string;
  detail: string;
};

const sidebarGroupLabels = new Set(sidebarItems.flatMap((group) => (group.label ? [group.label] : [])));

function getSubItemGroup(groupLabel: string | undefined, itemTitle: string) {
  return sidebarGroupLabels.has(itemTitle) ? (groupLabel ?? "Other") : itemTitle;
}

const searchItems: SearchItem[] = sidebarItems.flatMap((group) =>
  group.items.flatMap((item) => {
    if (item.subItems) {
      return item.subItems.map((sub) => ({
        id: sub.id,
        group: getSubItemGroup(group.label, item.title),
        label: sub.title,
        url: sub.url,
        icon: item.icon,
        disabled: sub.disabled,
        newTab: sub.newTab,
      }));
    }
    return [
      {
        id: item.id,
        group: group.label ?? "Other",
        label: item.title,
        keywords: item.keywords,
        url: item.url,
        icon: item.icon,
        disabled: item.disabled,
        newTab: item.newTab,
      },
    ];
  }),
);

function getAvailableItems(items: SearchItem[]) {
  return items.filter((item) => !item.disabled && !item.url.includes("coming-soon"));
}

const recommendations = getAvailableItems(searchItems);

function groupBy(items: SearchItem[]) {
  const groups = [...new Set(items.map((item) => item.group))];
  return groups.map((group) => ({
    group,
    items: items.filter((item) => item.group === group),
  }));
}

export function SearchDialog() {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [attention, setAttention] = React.useState<AttentionItem[] | null>(null);
  const router = useRouter();

  React.useEffect(() => {
    const down = (e: KeyboardEvent) => {
      if (e.key === "j" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((prev) => !prev);
      }
    };
    document.addEventListener("keydown", down);
    return () => document.removeEventListener("keydown", down);
  }, []);

  // Counts are read once per open. The dialog is not mounted as a poller.
  React.useEffect(() => {
    if (!open) return;
    let alive = true;
    setAttention(null);
    void (async () => {
      try {
        const [pending, taskList, pendingPage, progressPage, criticalPage] = await Promise.all([
          api.interceptPending(),
          api.tasks(),
          api.findingsPage({ page: 1, pageSize: 1, status: "pending" }),
          api.findingsPage({ page: 1, pageSize: 1, status: "in_progress" }),
          api.findingsPage({ page: 1, pageSize: 1, severity: "critical" }),
        ]);
        if (!alive) return;
        const paused = taskList.tasks.filter((task) => task.status === "paused").length;
        const review = pendingPage.total + progressPage.total;
        const critical = criticalPage.total;
        const items = (
          [
            pending.length > 0 && {
              href: "/system/intercept/approvals",
              dot: "bg-amber-500",
              title: swt("app.actNowApprovalsTitle"),
              detail: swt("app.actNowApprovalsDetail", { count: pending.length }),
            },
            paused > 0 && {
              href: "/function/tasks",
              dot: "bg-amber-500",
              title: swt("app.actNowPausedTitle"),
              detail: swt("app.actNowPausedDetail", { count: paused }),
            },
            review > 0 && {
              href: "/function/findings",
              dot: "bg-red-500",
              title: swt("app.actNowReviewTitle"),
              detail: swt("app.actNowReviewDetail", { count: review }),
            },
            critical > 0 && {
              href: "/function/findings",
              dot: "bg-rose-600",
              title: swt("app.actNowCriticalTitle"),
              detail: swt("app.actNowCriticalDetail", { count: critical }),
            },
          ] as const
        ).filter((item): item is AttentionItem => Boolean(item));
        setAttention(items);
      } catch {
        if (alive) setAttention([]);
      }
    })();
    return () => {
      alive = false;
    };
  }, [open, swLocale]);

  const handleOpenChange = (value: boolean) => {
    setOpen(value);
    if (!value) setQuery("");
  };

  const handleSelect = (item: SearchItem) => {
    if (item.disabled) return;
    handleOpenChange(false);
    if (item.newTab) {
      window.open(item.url, "_blank", "noopener,noreferrer");
    } else {
      router.push(item.url);
    }
  };

  const openAttention = (href: string) => {
    handleOpenChange(false);
    router.push(href);
  };

  const renderGroups = (items: SearchItem[]) =>
    groupBy(items).map(({ group, items: groupItems }, index) => (
      <React.Fragment key={group}>
        {index > 0 && <CommandSeparator />}
        <CommandGroup heading={group}>
          {groupItems.map((item) => (
            <CommandItem
              disabled={item.disabled}
              key={`${group}-${item.id}`}
              value={`${item.group} ${item.label} ${item.keywords ?? ""}`}
              onSelect={() => handleSelect(item)}
            >
              <span className="flex min-w-0 items-center gap-2">
                {item.icon && <item.icon />}
                <span className="truncate">{item.label}</span>
              </span>
            </CommandItem>
          ))}
        </CommandGroup>
      </React.Fragment>
    ));

  return (
    <>
      <button
        type="button"
        onClick={() => handleOpenChange(true)}
        className="flex h-8 w-full items-center gap-2 rounded-md border border-topbar-border bg-topbar-input px-2.5 text-[13px] text-topbar-muted transition-colors hover:border-zinc-600 hover:text-topbar-foreground"
      >
        <Search className="size-3.5 shrink-0" />
        <span className="flex-1 truncate text-left">{swt("english.e045")}</span>
        <kbd className="inline-flex h-5 select-none items-center gap-0.5 rounded border border-topbar-border px-1.5 font-medium text-[10px]">
          <span className="text-xs">⌘</span>J
        </kbd>
      </button>
      <CommandDialog open={open} onOpenChange={handleOpenChange} className="top-[12%]">
        <Command>
          <CommandInput placeholder={swt("english.e047")} value={query} onValueChange={setQuery} />
          {!query && (
            <div className="border-b px-2 py-2">
              <p className="px-2 py-1 font-medium text-muted-foreground text-xs">{swt("app.actNow")}</p>
              {attention === null ? (
                <p className="px-2 py-2 text-muted-foreground text-xs">{swt("common.loading")}</p>
              ) : attention.length === 0 ? (
                <p className="px-2 py-2 text-muted-foreground text-xs">{swt("app.actNowQuiet")}</p>
              ) : (
                attention.map((item) => (
                  <button
                    key={item.title}
                    type="button"
                    onClick={() => openAttention(item.href)}
                    className="flex w-full items-start gap-2.5 rounded-lg px-2 py-2 text-left transition-colors hover:bg-muted/60"
                  >
                    <span className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", item.dot)} />
                    <span className="min-w-0">
                      <span className="block font-medium text-[13px]">{item.title}</span>
                      <span className="block text-[11px] text-muted-foreground">{item.detail}</span>
                    </span>
                  </button>
                ))
              )}
            </div>
          )}
          <CommandList className={query ? undefined : "max-h-56"}>
            <CommandEmpty>{swt("english.e048")}</CommandEmpty>
            {query ? renderGroups(searchItems) : renderGroups(recommendations)}
          </CommandList>
          <div className="flex items-center gap-3 border-t px-3 py-2 text-[11px] text-muted-foreground">
            <span className="inline-flex items-center gap-1">
              <kbd className="rounded border bg-muted px-1 font-medium">↑↓</kbd>
              {swt("app.paletteMove")}
            </span>
            <span className="inline-flex items-center gap-1">
              <kbd className="rounded border bg-muted px-1 font-medium">↵</kbd>
              {swt("app.paletteOpen")}
            </span>
            <span className="inline-flex items-center gap-1">
              <kbd className="rounded border bg-muted px-1 font-medium">esc</kbd>
              {swt("app.paletteClose")}
            </span>
          </div>
        </Command>
      </CommandDialog>
    </>
  );
}
