"use client";
import * as React from "react";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { ChevronRight, MailIcon, PlusCircleIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  useSidebar,
} from "@/components/ui/sidebar";
import type { TooltipContent } from "@/components/ui/tooltip";
import { usePolling } from "@/hooks/use-polling";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type {
  NavBadge,
  NavGroup,
  NavMainItem,
  NavMainLinkItem,
  NavMainParentItem,
} from "@/navigation/sidebar/sidebar-items";

interface NavMainProps {
  readonly items: readonly NavGroup[];
}
interface NavItemProps {
  readonly item: NavMainItem;
  readonly isItemActive: (item: NavMainItem) => boolean;
  readonly isSubItemActive: (url: string) => boolean;
  readonly isSubmenuOpen: (item: NavMainParentItem) => boolean;
}

interface NavLinkItemProps {
  readonly item: NavMainLinkItem;
  readonly isActive: boolean;
  readonly showIconFallback: boolean;
}

interface NavLinkIconProps {
  readonly item: NavMainLinkItem;
  readonly showFallback: boolean;
}

interface NavDropdownItemProps {
  readonly item: NavMainParentItem;
  readonly isActive: boolean;
  readonly isSubItemActive: (url: string) => boolean;
}

interface NavCollapsibleItemProps {
  readonly item: NavMainParentItem;
  readonly isActive: boolean;
  readonly defaultOpen: boolean;
  readonly isSubItemActive: (url: string) => boolean;
}

function CollapsedIconFallback({ title }: { title: string }) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  return (
    <span className="flex size-4 shrink-0 items-center justify-center rounded-xs font-medium text-[10px] outline">
      {title.slice(0, 1)}
    </span>
  );
}

function hasSubItems(item: NavMainItem): item is NavMainParentItem {
  return Boolean(item.subItems?.length);
}

const ADVANCED_OPEN_KEY = "cw.sidebar.advanced.open";

// Tooltip for a nav link: the description always, plus the label while the sidebar is an icon rail.
function itemTooltip(item: NavMainLinkItem, iconRail: boolean): React.ComponentProps<typeof TooltipContent> | string {
  if (!item.description) return item.title;
  return {
    hidden: false,
    className: "max-w-60 flex-col items-start gap-0.5",
    children: (
      <>
        {iconRail && <span className="font-medium">{item.title}</span>}
        <span>{item.description}</span>
      </>
    ),
  };
}

interface CollapsibleNavGroupProps {
  readonly group: NavGroup;
  readonly hasActive: boolean;
  readonly children: React.ReactNode;
}

// A group that starts closed, remembers the choice in this browser and opens itself when the
// current page belongs to it.
function CollapsibleNavGroup({ group, hasActive, children }: CollapsibleNavGroupProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const { state, isMobile } = useSidebar();
  const iconRail = state === "collapsed" && !isMobile;
  const [open, setOpen] = React.useState(false);

  React.useEffect(() => {
    try {
      if (window.localStorage.getItem(ADVANCED_OPEN_KEY) === "1") setOpen(true);
    } catch {
      // Storage can be unavailable; the group simply starts closed.
    }
  }, []);

  React.useEffect(() => {
    if (hasActive) setOpen(true);
  }, [hasActive]);

  const handleOpenChange = (value: boolean) => {
    setOpen(value);
    try {
      window.localStorage.setItem(ADVANCED_OPEN_KEY, value ? "1" : "0");
    } catch {
      // Not persisted; the state still applies for this page view.
    }
  };

  return (
    <SidebarGroup>
      <Collapsible open={open || iconRail} onOpenChange={handleOpenChange} className="group/collapsible">
        <SidebarGroupLabel asChild className="group-data-[collapsible=icon]:pointer-events-none">
          <CollapsibleTrigger className="w-full cursor-pointer hover:bg-sidebar-accent hover:text-sidebar-accent-foreground">
            {group.label}
            <ChevronRight className="ml-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
          </CollapsibleTrigger>
        </SidebarGroupLabel>
        <CollapsibleContent>
          <SidebarGroupContent>
            <SidebarMenu>{children}</SidebarMenu>
          </SidebarGroupContent>
        </CollapsibleContent>
      </Collapsible>
    </SidebarGroup>
  );
}

export function NavMain({ items }: NavMainProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const rawPath = usePathname();
  // Decode percent-encoded path segments before comparing navigation URLs so active highlighting works.
  const path = (() => {
    try {
      return decodeURIComponent(rawPath).replace(/\/+$/, "");
    } catch {
      return rawPath.replace(/\/+$/, "");
    }
  })();
  const activeUrl = items
    .flatMap((group) => group.items.flatMap((item) => item.subItems ?? [item]))
    .filter((item) => item.url && (path === item.url || path.startsWith(`${item.url}/`)))
    .sort((a, b) => (b.url?.length ?? 0) - (a.url?.length ?? 0))[0]?.url;

  const isItemActive = (item: NavMainItem) => {
    if (hasSubItems(item)) {
      return item.subItems.some((sub) => path.startsWith(sub.url));
    }

    return item.url === activeUrl;
  };

  const isSubItemActive = (url: string) => {
    return activeUrl === url;
  };

  const isSubmenuOpen = (item: NavMainParentItem) => {
    return item.subItems.some((sub) => path.startsWith(sub.url));
  };

  return (
    <>
      {items.map((group) => {
        const entries = group.items.map((item) => (
          <NavItem
            key={item.id}
            item={item}
            isItemActive={isItemActive}
            isSubItemActive={isSubItemActive}
            isSubmenuOpen={isSubmenuOpen}
          />
        ));
        if (group.collapsible) {
          return (
            <CollapsibleNavGroup key={group.id} group={group} hasActive={group.items.some(isItemActive)}>
              {entries}
            </CollapsibleNavGroup>
          );
        }
        return (
          <SidebarGroup key={group.id}>
            {group.label && (
              <SidebarGroupLabel className="group-data-[collapsible=icon]:pointer-events-none">
                {group.label}
              </SidebarGroupLabel>
            )}
            <SidebarGroupContent>
              <SidebarMenu>{entries}</SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        );
      })}
    </>
  );
}

function NavItem({ item, isItemActive, isSubItemActive, isSubmenuOpen }: NavItemProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const { state, isMobile } = useSidebar();
  const isCollapsedDesktop = state === "collapsed" && !isMobile;

  if (!hasSubItems(item)) {
    return <NavLinkItem item={item} isActive={isItemActive(item)} showIconFallback={isCollapsedDesktop} />;
  }

  if (isCollapsedDesktop) {
    return <NavDropdownItem item={item} isActive={isItemActive(item)} isSubItemActive={isSubItemActive} />;
  }

  return (
    <NavCollapsibleItem
      item={item}
      isActive={isItemActive(item)}
      defaultOpen={isSubmenuOpen(item)}
      isSubItemActive={isSubItemActive}
    />
  );
}

// InterceptPendingBadge polls /api/intercept/pending every 5 s and shows a red
// count badge when there are unresolved approval requests.
function InterceptPendingBadge() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [count, setCount] = React.useState(0);
  // This badge is mounted on every page, so its poll is the most frequent always-on request in the
  // app. usePolling pauses it while the tab is backgrounded.
  usePolling(() => {
    api
      .interceptPending()
      .then((list) => setCount(list.length))
      .catch(() => {});
  }, 5000);
  if (count === 0) return null;
  return (
    <SidebarMenuBadge className="flex h-4.5 min-w-[18px] items-center justify-center rounded-full bg-amber-500 px-1 text-[10px] font-semibold leading-none text-white shadow-sm ring-1 ring-amber-400/40">
      <span aria-hidden="true">{count > 99 ? "99+" : count}</span>
      <span className="sr-only">
        {count === 1 ? swt("nav.approvalsWaitingOne") : swt("nav.approvalsWaitingMany", { p0: count })}
      </span>
    </SidebarMenuBadge>
  );
}

function NavLinkItem({ item, isActive, showIconFallback }: NavLinkItemProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const descriptionId = `nav-desc-${item.id}`;

  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        asChild
        aria-disabled={item.disabled}
        tooltip={itemTooltip(item, showIconFallback)}
        isActive={isActive}
      >
        <Link
          prefetch={false}
          aria-describedby={item.description ? descriptionId : undefined}
          href={item.url}
          target={item.newTab ? "_blank" : undefined}
          rel={item.newTab ? "noreferrer" : undefined}
        >
          <NavLinkIcon item={item} showFallback={showIconFallback} />
          <span>{item.title}</span>
        </Link>
      </SidebarMenuButton>
      {item.description && (
        <span id={descriptionId} className="sr-only">
          {item.description}
        </span>
      )}
      {item.id === "approvals" ? <InterceptPendingBadge /> : <NavItemBadge badge={item.badge} />}
    </SidebarMenuItem>
  );
}

function NavLinkIcon({ item, showFallback }: NavLinkIconProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const Icon = item.icon;

  if (Icon) {
    return <Icon />;
  }

  if (showFallback) {
    return <CollapsedIconFallback title={item.title} />;
  }

  return null;
}

function NavDropdownItem({ item, isActive, isSubItemActive }: NavDropdownItemProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const Icon = item.icon;

  return (
    <SidebarMenuItem>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <SidebarMenuButton tooltip={item.title} isActive={isActive} disabled={item.disabled}>
            {Icon ? <Icon /> : <CollapsedIconFallback title={item.title} />}
            <span>{item.title}</span>
          </SidebarMenuButton>
        </DropdownMenuTrigger>

        <DropdownMenuContent side="right" align="start" sideOffset={12} className="w-48">
          <DropdownMenuGroup>
            {item.subItems.map((subItem) => {
              const SubIcon = subItem.icon;

              return (
                <DropdownMenuItem key={subItem.id} asChild disabled={subItem.disabled}>
                  <Link
                    prefetch={false}
                    href={subItem.url}
                    target={subItem.newTab ? "_blank" : undefined}
                    rel={subItem.newTab ? "noreferrer" : undefined}
                    aria-current={isSubItemActive(subItem.url) ? "page" : undefined}
                    className="flex items-center gap-2"
                  >
                    {SubIcon && <SubIcon />}
                    <span>{subItem.title}</span>
                  </Link>
                </DropdownMenuItem>
              );
            })}
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </SidebarMenuItem>
  );
}

function NavCollapsibleItem({ item, isActive, defaultOpen, isSubItemActive }: NavCollapsibleItemProps) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  const Icon = item.icon;

  return (
    <Collapsible asChild defaultOpen={defaultOpen} className="group/collapsible">
      <SidebarMenuItem>
        <CollapsibleTrigger asChild>
          <SidebarMenuButton tooltip={item.title} isActive={isActive} disabled={item.disabled}>
            {Icon && <Icon />}
            <span>{item.title}</span>
            <ChevronRight className="ml-auto transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
          </SidebarMenuButton>
        </CollapsibleTrigger>
        <NavItemBadge badge={item.badge} />

        <CollapsibleContent>
          <SidebarMenuSub>
            {item.subItems.map((subItem) => {
              const SubIcon = subItem.icon;

              return (
                <SidebarMenuSubItem key={subItem.id}>
                  <SidebarMenuSubButton
                    asChild
                    aria-disabled={subItem.disabled}
                    isActive={isSubItemActive(subItem.url)}
                  >
                    <Link
                      prefetch={false}
                      href={subItem.url}
                      target={subItem.newTab ? "_blank" : undefined}
                      rel={subItem.newTab ? "noreferrer" : undefined}
                    >
                      {SubIcon && <SubIcon />}
                      <span>{subItem.title}</span>
                    </Link>
                  </SidebarMenuSubButton>
                </SidebarMenuSubItem>
              );
            })}
          </SidebarMenuSub>
        </CollapsibleContent>
      </SidebarMenuItem>
    </Collapsible>
  );
}

function NavItemBadge({ badge }: { badge?: NavBadge }) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  if (!badge) {
    return null;
  }

  return (
    <SidebarMenuBadge
      className={cn(
        "rounded-sm border capitalize",
        badge === "new" &&
          "border-green-600 text-green-700 dark:text-green-400 peer-hover/menu-button:text-green-600 peer-data-active/menu-button:text-green-600",
        badge === "soon" && "border-muted-foreground text-muted-foreground",
      )}
    >
      {badge}
    </SidebarMenuBadge>
  );
}
