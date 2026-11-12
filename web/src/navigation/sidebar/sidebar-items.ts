import {
  Activity,
  Ban,
  BellRing,
  BookOpen,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  FolderOpen,
  FolderSync,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
} from "lucide-react";

import { translate as swt } from "@/i18n/runtime";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  // One-line explanation shown as a tooltip and exposed as the accessible description.
  description?: string;
  // Extra search terms (previous names) that are never displayed.
  keywords?: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  // Pinned groups render below the scrolling list so they stay reachable on short screens.
  pinned?: boolean;
  // Collapsible groups start closed and open on their own when the current route is inside them.
  collapsible?: boolean;
  items: NavMainItem[];
}

const link = (
  id: string,
  title: () => string,
  url: string,
  icon: LucideIcon,
  description: () => string,
  keywords?: () => string,
): NavMainLinkItem => ({
  id,
  get title() {
    return title();
  },
  get description() {
    return description();
  },
  get keywords() {
    return keywords?.();
  },
  url,
  icon,
});

export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    get label() {
      return swt("english.e197");
    },
    items: [
      link(
        "dashboard",
        () => swt("interface.m2871"),
        "/dashboard",
        LayoutDashboard,
        () => swt("nav.descDashboard"),
      ),
      link(
        "tasks",
        () => swt("interface.m0190"),
        "/function/tasks",
        Target,
        () => swt("nav.descTasks"),
      ),
      link(
        "findings",
        () => swt("interface.m0188"),
        "/function/findings",
        Bug,
        () => swt("nav.descFindings"),
      ),
      link(
        "chat",
        () => swt("interface.m2872"),
        "/chat",
        MessageSquare,
        () => swt("nav.descChat"),
      ),
      link(
        "assets",
        () => swt("nav.assets"),
        "/function/assets",
        Network,
        () => swt("nav.descAssets"),
        () => swt("nav.kwAssets"),
      ),
      link(
        "sync",
        () => swt("nav.sync"),
        "/function/sync",
        FolderSync,
        () => swt("nav.descSync"),
        () => swt("nav.kwSync"),
      ),
      link(
        "workspace",
        () => swt("nav.workspace"),
        "/function/workspace",
        FolderOpen,
        () => swt("nav.descWorkspace"),
        () => swt("nav.kwWorkspace"),
      ),
    ],
  },
  {
    id: 2,
    get label() {
      return swt("english.e198");
    },
    items: [
      link(
        "traffic",
        () => swt("interface.m1067"),
        "/function/traffic",
        Activity,
        () => swt("nav.descTraffic"),
      ),
      link(
        "commands",
        () => swt("nav.commands"),
        "/function/commands",
        Terminal,
        () => swt("nav.descCommands"),
        () => swt("nav.kwCommands"),
      ),
      link(
        "llm-records",
        () => swt("nav.llmRecords"),
        "/function/llm-records",
        Radio,
        () => swt("nav.descLlmRecords"),
        () => swt("nav.kwLlmRecords"),
      ),
      link(
        "logs",
        () => swt("interface.m2873"),
        "/system/logs",
        ScrollText,
        () => swt("nav.descLogs"),
      ),
    ],
  },
  {
    id: 3,
    get label() {
      return swt("english.e199");
    },
    items: [
      link(
        "llm",
        () => swt("nav.llm"),
        "/system/llm",
        Brain,
        () => swt("nav.descLlm"),
        () => swt("nav.kwLlm"),
      ),
      link(
        "notify",
        () => swt("interface.m1541"),
        "/system/notify",
        BellRing,
        () => swt("nav.descNotify"),
      ),
      link(
        "settings",
        () => swt("interface.m1644"),
        "/system/settings",
        Settings2,
        () => swt("nav.descSettings"),
      ),
    ],
  },
  {
    id: 4,
    get label() {
      return swt("english.e200");
    },
    items: [
      link(
        "intercept",
        () => swt("nav.intercept"),
        "/system/intercept",
        ShieldAlert,
        () => swt("nav.descIntercept"),
        () => swt("nav.kwIntercept"),
      ),
      link(
        "asset-intercept",
        () => swt("nav.assetIntercept"),
        "/system/intercept/assets",
        Ban,
        () => swt("nav.descAssetIntercept"),
        () => swt("nav.kwAssetIntercept"),
      ),
      link(
        "approvals",
        () => swt("nav.approvals"),
        "/system/intercept/approvals",
        ClipboardList,
        () => swt("nav.descApprovals"),
        () => swt("nav.kwApprovals"),
      ),
    ],
  },
  {
    id: 6,
    collapsible: true,
    get label() {
      return swt("nav.groupAdvanced");
    },
    items: [
      link(
        "agents",
        () => swt("english.e194"),
        "/system/agents",
        Bot,
        () => swt("nav.descAgents"),
      ),
      link(
        "mcp",
        () => "MCP",
        "/system/mcp",
        Plug,
        () => swt("nav.descMcp"),
      ),
      link(
        "skills",
        () => swt("english.e195"),
        "/system/skills",
        Sparkles,
        () => swt("nav.descSkills"),
      ),
      link(
        "tools",
        () => swt("english.e196"),
        "/system/tools",
        Wrench,
        () => swt("nav.descTools"),
      ),
    ],
  },
  {
    id: 5,
    pinned: true,
    get label() {
      return swt("english.e192");
    },
    items: [
      link(
        "docs",
        () => swt("english.e193"),
        "/docs",
        BookOpen,
        () => swt("nav.descDocs"),
      ),
    ],
  },
];

// Resolves the sidebar entry (and its group) that owns a pathname; the longest matching url wins so
// /system/intercept/approvals is not claimed by /system/intercept.
export function findNavEntry(pathname: string): { group: NavGroup; item: NavMainLinkItem | NavSubItem } | undefined {
  const route = pathname.replace(/\/+$/, "");
  let best: { group: NavGroup; item: NavMainLinkItem | NavSubItem } | undefined;
  for (const group of sidebarItems) {
    for (const main of group.items) {
      const entries: (NavMainLinkItem | NavSubItem)[] = main.subItems ?? [main as NavMainLinkItem];
      for (const item of entries) {
        if (!item.url || !(route === item.url || route.startsWith(`${item.url}/`))) continue;
        if (!best || item.url.length > best.item.url.length) best = { group, item };
      }
    }
  }
  return best;
}
