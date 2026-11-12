"use client";

import * as React from "react";

import Link from "next/link";

import {
  ArrowUpRightIcon,
  BugIcon,
  ChevronRightIcon,
  ClockIcon,
  DownloadIcon,
  InfoIcon,
  SearchIcon,
  ShieldAlertIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { CountLabel } from "@/components/console/count-label";
import { EmptyState } from "@/components/console/empty-state";
import { KpiCard } from "@/components/console/kpi-card";
import { ErrorState } from "@/components/error-state";
import { FindingRetestDialog } from "@/components/finding-retest-dialog";
import { PageHeader } from "@/components/page-header";
import { StatusBadge } from "@/components/status-badge";
import { TablePagination } from "@/components/table-pagination";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { pollWhileVisible } from "@/hooks/use-polling";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";
import { statusMeta } from "@/lib/status";
import type {
  ActiveFindingRetest,
  Finding,
  FindingAssetNode,
  FindingGroup,
  FindingStats,
  FindingStatus,
  Severity,
} from "@/lib/types";
import { cn } from "@/lib/utils";

import { AssetTree, assetPathOf } from "./_components/asset-tree";
import {
  FINDING_STATUSES,
  type FindingEdit,
  type FindingReport,
  FindingsTable,
  findingRowKey,
  fmtTime,
  isSameFinding,
  SEVERITIES,
  UNASSIGNED_TASK,
} from "./_components/findings-table";

const FINDING_LIST_PREFERENCE_KEY = "counterweight_finding_list_preferences";

// List modes: flat cross-task table (default), grouped collapsible task sections,
// or asset tree on the left with subtree findings on the right.
type FindingView = "flat" | "grouped" | "asset";

const FINDING_VIEWS: FindingView[] = ["flat", "grouped", "asset"];

// The asset tree is a one-time snapshot; unlike other views, it does not poll.
// Reload on entry, filter changes, or local finding mutations.
interface AssetTreeState {
  nodes: FindingAssetNode[];
  findingTotal: number;
  truncated: boolean;
  droppedKinds: string[];
  loaded: boolean;
  loading: boolean;
  error: string | null;
}

const EMPTY_ASSET_TREE: AssetTreeState = {
  nodes: [],
  findingTotal: 0,
  truncated: false,
  droppedKinds: [],
  loaded: false,
  loading: false,
  error: null,
};

// Each expanded task group maintains independent pagination state.
interface GroupFindingsState {
  items: Finding[];
  total: number;
  // page and pageSize describe the rows in items; they change only when a request succeeds.
  page: number;
  pageSize: number;
  loaded: boolean;
  loading: boolean;
  // Set when a request for a different page or page size failed, so items do not belong to what was asked for.
  error: { message: string; page: number; pageSize: number } | null;
}

// Keep flat page state outside the snapshot so filter changes reset it and trigger loading.
interface FlatFindingsState {
  items: Finding[];
  total: number;
  loaded: boolean;
  loading: boolean;
  // Identity of the query that produced items (page, page size, filters, asset scope); empty until loaded.
  queryKey: string;
  // Message of the last failed request whose query differs from the one that produced items; null when healthy.
  error: string | null;
}

const EMPTY_FLAT_STATE: FlatFindingsState = {
  items: [],
  total: 0,
  loaded: false,
  loading: false,
  queryKey: "",
  error: null,
};

function findingGroupKey(group: FindingGroup) {
  return group.task_id === null ? UNASSIGNED_TASK : String(group.task_id);
}

const EMPTY_STATS: FindingStats = {
  total: 0,
  pending: 0,
  critical: 0,
  high: 0,
  medium: 0,
  low: 0,
  vulnclasses: [],
  tasks: [],
};

export default function FindingsPage() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [view, setView] = React.useState<FindingView>("flat");
  const [severity, setSeverity] = React.useState<"all" | Severity>("all");
  const [status, setStatus] = React.useState<"all" | FindingStatus>("all");
  const [vulnclass, setVulnclass] = React.useState<string>("all");
  const [task, setTask] = React.useState<string>("all");
  const [sort, setSort] = React.useState<"severity" | "time">("severity");
  const [search, setSearch] = React.useState("");
  const [query, setQuery] = React.useState("");
  const [expanded, setExpanded] = React.useState<string | null>(null);
  const [flat, setFlat] = React.useState<FlatFindingsState>(EMPTY_FLAT_STATE);
  const [flatPage, setFlatPage] = React.useState(1);
  const [flatPageSize, setFlatPageSize] = React.useState(20);
  const [assetTree, setAssetTree] = React.useState<AssetTreeState>(EMPTY_ASSET_TREE);
  const [assetScope, setAssetScope] = React.useState<string | null>(null);
  const [groups, setGroups] = React.useState<FindingGroup[]>([]);
  const [groupTotal, setGroupTotal] = React.useState(0);
  const [groupsLoaded, setGroupsLoaded] = React.useState(false);
  const [groupsError, setGroupsError] = React.useState<string | null>(null);
  const [expandedGroups, setExpandedGroups] = React.useState<Set<string>>(() => new Set());
  const [groupFindings, setGroupFindings] = React.useState<Record<string, GroupFindingsState>>({});
  const [total, setTotal] = React.useState(0);
  const [stats, setStats] = React.useState<FindingStats>(EMPTY_STATS);
  const [statsLoaded, setStatsLoaded] = React.useState(false);
  const [preferencesHydrated, setPreferencesHydrated] = React.useState(false);
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(10);
  const [deepenFinding, setDeepenFinding] = React.useState<Finding | null>(null);
  const [retestFinding, setRetestFinding] = React.useState<Finding | null>(null);
  const [activeRetests, setActiveRetests] = React.useState<Record<string, ActiveFindingRetest>>({});
  const activeRetestFingerprint = Object.values(activeRetests)
    .map((item) => item.id)
    .join(",");
  const retestRefreshVersion = React.useRef(0);
  const [deepenDescription, setDeepenDescription] = React.useState("");
  const [deepening, setDeepening] = React.useState(false);
  const filterFingerprint = JSON.stringify([severity, status, vulnclass, task, sort, query]);
  const activeFilterFingerprint = React.useRef(filterFingerprint);
  activeFilterFingerprint.current = filterFingerprint;

  // One lightweight request covers all rows/views instead of fetching retest history per row; poll after completion.
  React.useEffect(() => {
    let disposed = false;
    let failed = false;
    let timer: ReturnType<typeof setTimeout>;
    async function refreshRetests() {
      const version = retestRefreshVersion.current;
      try {
        const rows = await api.activeFindingRetests();
        if (disposed || version !== retestRefreshVersion.current) return;
        setActiveRetests(Object.fromEntries(rows.map((item) => [item.finding_id, item])));
        failed = false;
      } catch (error) {
        if (!disposed && !failed) toast.error(swt("interface.m0371", { p0: (error as Error).message }));
        failed = true;
      } finally {
        if (!disposed) timer = setTimeout(() => void refreshRetests(), 3000);
      }
    }
    void refreshRetests();
    return () => {
      disposed = true;
      clearTimeout(timer);
    };
  }, []);

  React.useEffect(() => {
    const raw = getLocalStorageValue(FINDING_LIST_PREFERENCE_KEY);
    if (raw) {
      try {
        const parsed = JSON.parse(raw) as {
          view?: unknown;
          severity?: unknown;
          status?: unknown;
          vulnclass?: unknown;
          task?: unknown;
          sort?: unknown;
        };
        if (FINDING_VIEWS.includes(parsed.view as FindingView)) setView(parsed.view as FindingView);
        if (parsed.severity === "all" || SEVERITIES.includes(parsed.severity as Severity)) {
          setSeverity(parsed.severity as "all" | Severity);
        }
        if (parsed.status === "all" || FINDING_STATUSES.includes(parsed.status as FindingStatus)) {
          setStatus(parsed.status as "all" | FindingStatus);
        }
        if (typeof parsed.vulnclass === "string" && parsed.vulnclass) setVulnclass(parsed.vulnclass);
        if (typeof parsed.task === "string" && parsed.task) setTask(parsed.task);
        if (parsed.sort === "severity" || parsed.sort === "time") setSort(parsed.sort);
      } catch {
        // Ignore malformed or legacy preferences and retain the defaults.
      }
    }
    setPreferencesHydrated(true);
  }, []);

  React.useEffect(() => {
    if (!preferencesHydrated) return;
    setLocalStorageValue(
      FINDING_LIST_PREFERENCE_KEY,
      JSON.stringify({ view, severity, status, vulnclass, task, sort }),
    );
  }, [preferencesHydrated, severity, sort, status, task, view, vulnclass]);

  React.useEffect(() => {
    const timer = window.setTimeout(() => setQuery(search.trim()), 300);
    return () => window.clearTimeout(timer);
  }, [search]);

  // setFindings updates the same finding in both view caches to prevent stale values on switching.
  const setFindings = React.useCallback((update: (current: Finding[]) => Finding[]) => {
    setFlat((current) => ({ ...current, items: update(current.items) }));
    setGroupFindings((current) => {
      const next: Record<string, GroupFindingsState> = {};
      for (const [key, state] of Object.entries(current)) {
        next[key] = { ...state, items: update(state.items) };
      }
      return next;
    });
  }, []);

  // Track export selections by independent finding_id and retain selections across pages.
  const [selectedIds, setSelectedIds] = React.useState<Set<string>>(() => new Set());
  // Export dialog: current/all/selected scope and single Markdown/ZIP Markdown/CSV/JSON format.
  const [exportOpen, setExportOpen] = React.useState(false);
  const [exportScope, setExportScope] = React.useState<"filtered" | "all" | "selected">("filtered");
  const [exportFormat, setExportFormat] = React.useState<"md-single" | "md-zip" | "csv" | "json" | "pdf" | "sarif">(
    "md-single",
  );
  const [exporting, setExporting] = React.useState(false);

  const toggleSelected = React.useCallback((id: string, checked: boolean) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);

  const toggleSelectedPage = React.useCallback((ids: string[], checked: boolean) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      for (const id of ids) {
        if (checked) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  }, []);

  // Default export scope to selected records when any are checked; otherwise use current filters.
  function openExport() {
    setExportScope(selectedIds.size > 0 ? "selected" : "filtered");
    setExportOpen(true);
  }

  async function doExport() {
    setExporting(true);
    try {
      await api.exportFindings({
        format: exportFormat,
        scope: exportScope,
        filters: { severity, status, vulnclass, task, query, sort },
        ids: [...selectedIds],
      });
      setExportOpen(false);
      toast.success(swt("interface.m0372"));
    } catch (e) {
      toast.error(swt("interface.m0373", { p0: (e as Error).message }));
    } finally {
      setExporting(false);
    }
  }

  const flatRequest = React.useRef(0);
  const flatRefreshFailed = React.useRef(false);
  const groupsRefreshFailed = React.useRef(false);
  // Identity of the query behind the group headers currently on screen (page, page size, filters).
  const groupsQueryKey = React.useRef("");
  const groupRefreshFailed = React.useRef<Record<string, boolean>>({});
  const assetTreeRequest = React.useRef(0);
  const groupRequests = React.useRef<Record<string, number>>({});
  const groupsRequest = React.useRef(0);
  const flatStateRef = React.useRef(flat);
  const expandedGroupsRef = React.useRef(expandedGroups);
  const groupFindingsRef = React.useRef(groupFindings);
  const visibleGroupKeysRef = React.useRef<Set<string>>(new Set());
  flatStateRef.current = flat;
  expandedGroupsRef.current = expandedGroups;
  groupFindingsRef.current = groupFindings;
  visibleGroupKeysRef.current = new Set(groups.map(findingGroupKey));

  // The asset view reuses flat-list state with an additional selected-subtree filter.
  const activeAssetScope = view === "asset" ? assetScope : null;

  // loadFlat fetches the current page; backend task filtering shares the grouped view's filters.
  const loadFlat = React.useCallback(async () => {
    const requestFilter = filterFingerprint;
    if (activeFilterFingerprint.current !== requestFilter) return;
    const request = ++flatRequest.current;
    const queryKey = JSON.stringify([
      flatPage,
      flatPageSize,
      severity,
      status,
      vulnclass,
      task,
      query,
      sort,
      activeAssetScope,
    ]);
    setFlat((current) => ({ ...current, loading: true }));
    try {
      const result = await api.findingsPage({
        page: flatPage,
        pageSize: flatPageSize,
        severity,
        status,
        vulnclass,
        task,
        query,
        sort,
        assetScope: activeAssetScope ?? undefined,
      });
      if (request !== flatRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      flatRefreshFailed.current = false;
      setFlat({ items: result.items, total: result.total, loaded: true, loading: false, queryKey, error: null });
    } catch (e) {
      if (request !== flatRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      const message = (e as Error).message;
      // A failed background refresh of the query already on screen keeps its rows and reports once, not on
      // every poll. Any other failed query must not leave rows from a different query visible.
      const sameQuery = flatStateRef.current.loaded && flatStateRef.current.queryKey === queryKey;
      if (sameQuery && !flatRefreshFailed.current) {
        toast.error(swt("states.findingsRefreshFailed", { p0: message }));
      }
      flatRefreshFailed.current = true;
      setFlat((current) => ({ ...current, loading: false, error: sameQuery ? null : message }));
    }
  }, [
    swLocale,
    activeAssetScope,
    filterFingerprint,
    flatPage,
    flatPageSize,
    severity,
    status,
    vulnclass,
    task,
    query,
    sort,
  ]);

  // loadAssetTree loads the whole tree, independent of selected nodes so selection does not collapse it into a chain.
  // Do not include assetScope here.
  const loadAssetTree = React.useCallback(async () => {
    const requestFilter = filterFingerprint;
    if (activeFilterFingerprint.current !== requestFilter) return;
    const request = ++assetTreeRequest.current;
    setAssetTree((current) => ({ ...current, loading: true }));
    try {
      const result = await api.findingAssetTree({ severity, status, vulnclass, task, query, sort });
      if (request !== assetTreeRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      setAssetTree({
        nodes: result.nodes ?? [],
        findingTotal: result.finding_total ?? 0,
        truncated: Boolean(result.truncated),
        droppedKinds: result.dropped_kinds ?? [],
        loaded: true,
        loading: false,
        error: null,
      });
    } catch (e) {
      if (request !== assetTreeRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      // The tree depends only on the filters, which reset it on change, so rows on screen always match.
      setAssetTree((current) => ({ ...current, loading: false, error: current.loaded ? null : (e as Error).message }));
      toast.error(swt("interface.m0374", { p0: (e as Error).message }));
    }
  }, [swLocale, filterFingerprint, severity, status, vulnclass, task, query, sort]);

  const refreshGroups = React.useCallback(async () => {
    const requestFilter = filterFingerprint;
    if (activeFilterFingerprint.current !== requestFilter) return;
    const request = ++groupsRequest.current;
    const queryKey = JSON.stringify([page, pageSize, severity, status, vulnclass, task, query, sort]);
    try {
      const result = await api.findingGroups({
        page,
        pageSize,
        severity,
        status,
        vulnclass,
        task,
        query,
        sort,
      });
      if (request !== groupsRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      setGroups(result.items);
      setGroupTotal(result.total);
      groupsQueryKey.current = queryKey;
      setGroupsLoaded(true);
      setGroupsError(null);
      groupsRefreshFailed.current = false;
      setTotal(result.finding_total);
    } catch (e) {
      if (request !== groupsRequest.current || activeFilterFingerprint.current !== requestFilter) return;
      // A failed background refresh of the page already on screen keeps it and reports once. A failure for
      // any other page, page size or filter set must not leave the previous headers visible.
      const sameQuery = groupsQueryKey.current === queryKey;
      if (sameQuery) {
        if (!groupsRefreshFailed.current) {
          toast.error(swt("states.findingsRefreshFailed", { p0: (e as Error).message }));
        }
      } else {
        setGroupsError((e as Error).message);
      }
      groupsRefreshFailed.current = true;
    }
  }, [swLocale, filterFingerprint, page, pageSize, severity, status, vulnclass, task, query, sort]);

  const loadGroup = React.useCallback(
    async (key: string, groupPage: number, groupPageSize: number) => {
      const request = (groupRequests.current[key] ?? 0) + 1;
      const requestFilter = filterFingerprint;
      if (activeFilterFingerprint.current !== requestFilter) return;
      groupRequests.current[key] = request;
      setGroupFindings((current) => ({
        ...current,
        [key]: {
          items: current[key]?.items ?? [],
          total: current[key]?.total ?? 0,
          page: current[key]?.page ?? groupPage,
          pageSize: current[key]?.pageSize ?? groupPageSize,
          loaded: current[key]?.loaded ?? false,
          loading: true,
          error: current[key]?.error ?? null,
        },
      }));
      try {
        const result = await api.findingsPage({
          page: groupPage,
          pageSize: groupPageSize,
          severity,
          status,
          vulnclass,
          task: key,
          query,
          sort,
        });
        if (groupRequests.current[key] !== request || activeFilterFingerprint.current !== requestFilter) return;
        setGroupFindings((current) => ({
          ...current,
          [key]: {
            items: result.items,
            total: result.total,
            page: result.page,
            pageSize: result.page_size,
            loaded: true,
            loading: false,
            error: null,
          },
        }));
        groupRefreshFailed.current[key] = false;
      } catch (e) {
        if (groupRequests.current[key] !== request || activeFilterFingerprint.current !== requestFilter) return;
        const message = (e as Error).message;
        // Same page and size as the rows on screen: a background refresh, keep the rows and report once.
        // Otherwise the rows belong to another page, so show an error in their place.
        const shown = groupFindingsRef.current[key];
        const sameQuery = Boolean(shown?.loaded && shown.page === groupPage && shown.pageSize === groupPageSize);
        if (sameQuery && !groupRefreshFailed.current[key]) {
          toast.error(swt("states.findingsRefreshFailed", { p0: message }));
        }
        groupRefreshFailed.current[key] = true;
        setGroupFindings((current) => ({
          ...current,
          [key]: {
            ...(current[key] ?? {
              items: [],
              total: 0,
              page: groupPage,
              pageSize: groupPageSize,
              loaded: false,
              error: null,
            }),
            loading: false,
            error: sameQuery ? null : { message, page: groupPage, pageSize: groupPageSize },
          },
        }));
      }
    },
    [swLocale, filterFingerprint, severity, status, vulnclass, query, sort],
  );

  React.useEffect(() => {
    for (const [key, state] of Object.entries(groupFindings)) {
      if (!state.loaded || state.loading || state.error) continue;
      const lastPage = Math.max(1, Math.ceil(state.total / state.pageSize));
      if (state.page > lastPage) void loadGroup(key, lastPage, state.pageSize);
    }
  }, [groupFindings, loadGroup]);

  const toggleGroup = React.useCallback(
    (key: string) => {
      const opening = !expandedGroups.has(key);
      const next = new Set(expandedGroups);
      if (opening) next.add(key);
      else next.delete(key);
      setExpandedGroups(next);
      const state = groupFindings[key];
      if (opening && !state?.loaded && !state?.loading) {
        void loadGroup(key, state?.page ?? 1, state?.pageSize ?? 10);
      }
    },
    [expandedGroups, groupFindings, loadGroup],
  );

  // After inline changes, reload the flat page or grouped headers and the affected group.
  const refreshAfterMutation = React.useCallback(
    (finding: Finding, removed = false) => {
      if (view === "asset") {
        // The asset view does not poll, so recalculate its tree counts after mutations.
        void loadFlat();
        void loadAssetTree();
        return;
      }
      if (view === "flat") {
        // When deleting the last page's contents, the bounds-correction effect moves back and reloads.
        void loadFlat();
        return;
      }
      void refreshGroups();
      const key = finding.task_id ?? UNASSIGNED_TASK;
      const state = groupFindingsRef.current[key];
      if (state?.loaded) {
        const nextTotal = Math.max(0, state.total - (removed ? 1 : 0));
        const lastPage = Math.max(1, Math.ceil(nextTotal / state.pageSize));
        void loadGroup(key, Math.min(state.page, lastPage), state.pageSize);
      }
    },
    [loadAssetTree, loadFlat, loadGroup, refreshGroups, view],
  );

  // Reset every view's pagination and expansion when a shared finding filter changes.
  React.useEffect(() => {
    void filterFingerprint;
    setPage(1);
    setExpanded(null);
    setExpandedGroups(new Set());
    setGroupFindings({});
    setFlatPage(1);
    setGroupsLoaded(false);
    setGroupsError(null);
    groupsQueryKey.current = "";
    groupRefreshFailed.current = {};
    setFlat(EMPTY_FLAT_STATE);
    // Filter changes may remove the selected tree node; return to All assets.
    setAssetScope(null);
    setAssetTree(EMPTY_ASSET_TREE);
  }, [filterFingerprint]);

  // Changing the asset node selects a new result set; return to page one.
  React.useEffect(() => {
    void assetScope;
    setFlatPage(1);
  }, [assetScope]);

  // Fetch the asset tree on entry/filter changes, plus explicit reloads through
  // refreshAfterMutation after finding edits. Do not poll.
  React.useEffect(() => {
    if (!preferencesHydrated || view !== "asset") return;
    void activeRetestFingerprint; // Retest completion can change asset counts under status filters.
    void loadAssetTree();
  }, [activeRetestFingerprint, loadAssetTree, preferencesHydrated, view]);

  // Poll only the active view: flat current page, or grouped headers and all expanded groups with independent pages.
  // The asset view queries once; its navigation tree need not be recalculated every five seconds.
  // Wait for preference hydration before the first request to avoid an unnecessary default-view request.
  React.useEffect(() => {
    if (!preferencesHydrated) return;
    void activeRetestFingerprint; // Refresh resolution status after retests, including the nonpolling asset view.
    const refresh = () => {
      if (view === "flat" || view === "asset") {
        if (!flatStateRef.current.loading) void loadFlat();
        return;
      }
      void refreshGroups();
      for (const key of expandedGroupsRef.current) {
        if (!visibleGroupKeysRef.current.has(key)) continue;
        const state = groupFindingsRef.current[key];
        if (!state || state.loading) continue;
        // A group whose last request failed keeps retrying that same request.
        if (state.error) void loadGroup(key, state.error.page, state.error.pageSize);
        else if (state.loaded) void loadGroup(key, state.page, state.pageSize);
      }
    };
    refresh();
    if (view === "asset") return;
    const timer = pollWhileVisible(refresh, 5000);
    return () => timer();
  }, [activeRetestFingerprint, loadFlat, loadGroup, preferencesHydrated, refreshGroups, view]);

  React.useEffect(() => {
    if (groupsError) return;
    const lastPage = Math.max(1, Math.ceil(groupTotal / pageSize));
    if (page > lastPage) setPage(lastPage);
  }, [groupsError, groupTotal, page, pageSize]);

  React.useEffect(() => {
    if (!flat.loaded || flat.error) return;
    const lastPage = Math.max(1, Math.ceil(flat.total / flatPageSize));
    if (flatPage > lastPage) setFlatPage(lastPage);
  }, [flat.loaded, flat.error, flat.total, flatPage, flatPageSize]);

  // Whole-table aggregates (stat cards + vuln-class options) — independent of the
  // current page, so they stay exact.
  React.useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .findingStats()
        .then((s) => {
          if (alive) {
            setStats(s);
            setStatsLoaded(true);
          }
        })
        .catch(() => {
          // Keep the previous aggregate snapshot until the next poll.
        });
    };
    load();
    const t = pollWhileVisible(load, 5000);
    return () => {
      alive = false;
      t();
    };
  }, []);

  React.useEffect(() => {
    if (!statsLoaded) return;
    if (vulnclass !== "all" && !stats.vulnclasses.includes(vulnclass)) setVulnclass("all");
    if (
      task !== "all" &&
      task !== UNASSIGNED_TASK &&
      !(stats.tasks ?? []).some((option) => String(option.id) === task)
    ) {
      setTask("all");
    }
  }, [stats, statsLoaded, task, vulnclass]);

  // updateStatus optimistically flips one finding's triage state, reverting on error.
  const updateStatus = React.useCallback(
    async (f: Finding, next: FindingStatus) => {
      if (!f.finding_id || next === f.status) return;
      const prev = f.status;
      setFindings((cur) => cur.map((x) => (isSameFinding(x, f) ? { ...x, status: next } : x)));
      try {
        await api.setFindingStatus(f.finding_id, next);
        toast.success(swt("interface.m0375", { p0: statusMeta("finding", next).label }));
        // refresh stat cards (pending count) and drop the row if it no longer matches the status filter
        api
          .findingStats()
          .then(setStats)
          .catch(() => {
            // The row update remains valid even if the aggregate refresh fails.
          });
        if (status !== "all" && next !== status) {
          setFindings((cur) => cur.filter((x) => !isSameFinding(x, f)));
          setTotal((t) => Math.max(0, t - 1));
          setFlat((cur) => ({ ...cur, total: Math.max(0, cur.total - 1) }));
        }
        refreshAfterMutation(f);
      } catch (e) {
        setFindings((cur) => cur.map((x) => (isSameFinding(x, f) ? { ...x, status: prev } : x)));
        toast.error(swt("interface.m0376", { p0: (e as Error).message }));
      }
    },
    [swLocale, refreshAfterMutation, setFindings, status],
  );

  // Cache expanded reports by stable global row key. Lists omit large Markdown reports,
  // so fetch once by finding_id on expansion. Done with empty text means no report exists.
  const [reports, setReports] = React.useState<Record<string, FindingReport>>({});

  // Initialize the name/category/severity edit buffer from the expanded row, and clear it on collapse.
  // Only one row expands, so a single buffer suffices.
  const [edit, setEdit] = React.useState<FindingEdit | null>(null);
  const [saving, setSaving] = React.useState(false);

  // toggle opens/closes a row, initializes edits, and fetches its report by finding_id if not cached.
  const toggleRow = React.useCallback(
    (f: Finding) => {
      const key = findingRowKey(f);
      const willOpen = expanded !== key;
      setExpanded(willOpen ? key : null);
      if (!willOpen) {
        setEdit(null);
        return;
      }
      setEdit({ name: f.name ?? "", vulnclass: f.vulnclass, severity: f.severity });
      if (!f.finding_id || reports[key]) return;
      const fid = f.finding_id;
      setReports((r) => ({ ...r, [key]: { status: "loading", text: "" } }));
      api
        .getFinding(fid)
        .then((full) => setReports((r) => ({ ...r, [key]: { status: "done", text: full.report ?? "" } })))
        .catch(() => setReports((r) => ({ ...r, [key]: { status: "error", text: "" } })));
    },
    [expanded, reports],
  );

  // saveEdit saves name/category/severity, updates the list, and refreshes category/severity statistics.
  const saveEdit = React.useCallback(
    async (f: Finding) => {
      if (!f.finding_id || !edit) return;
      setSaving(true);
      try {
        const updated = await api.updateFinding(f.finding_id, {
          name: edit.name.trim(),
          vulnclass: edit.vulnclass.trim(),
          severity: edit.severity,
        });
        setFindings((cur) =>
          cur.map((x) =>
            isSameFinding(x, f)
              ? { ...x, name: updated.name, vulnclass: updated.vulnclass, severity: updated.severity }
              : x,
          ),
        );
        toast.success(swt("interface.m0377"));
        api
          .findingStats()
          .then(setStats)
          .catch(() => {
            // The edit remains valid even if the aggregate refresh fails.
          });
        refreshAfterMutation(f);
      } catch (e) {
        toast.error(swt("interface.m0267", { p0: (e as Error).message }));
      } finally {
        setSaving(false);
      }
    },
    [swLocale, edit, refreshAfterMutation, setFindings],
  );

  // deleteFinding requires confirmation, then removes the row, collapses it, and refreshes statistics.
  const deleteFinding = React.useCallback(
    async (f: Finding) => {
      if (!f.finding_id) return;
      try {
        await api.deleteFinding(f.finding_id);
        setFindings((cur) => cur.filter((x) => !isSameFinding(x, f)));
        setSelectedIds((current) => {
          const next = new Set(current);
          next.delete(f.finding_id as string);
          return next;
        });
        setTotal((t) => Math.max(0, t - 1));
        setFlat((cur) => ({ ...cur, total: Math.max(0, cur.total - 1) }));
        const rowKey = findingRowKey(f);
        setExpanded((cur) => (cur === rowKey ? null : cur));
        toast.success(swt("interface.m0378"));
        api
          .findingStats()
          .then(setStats)
          .catch(() => {
            // The deletion remains valid even if the aggregate refresh fails.
          });
        refreshAfterMutation(f, true);
      } catch (e) {
        toast.error(swt("interface.m0379", { p0: (e as Error).message }));
      }
    },
    [swLocale, refreshAfterMutation, setFindings],
  );

  const openDeepen = React.useCallback((f: Finding) => {
    setDeepenFinding(f);
    setDeepenDescription("");
  }, []);

  async function submitDeepen() {
    if (!deepenFinding?.finding_id || !deepenDescription.trim() || deepening) return;
    setDeepening(true);
    try {
      const result = await api.deepenFinding(deepenFinding.finding_id, deepenDescription.trim());
      toast.success(
        result.queued
          ? swt("interface.m0380", { p0: result.intent_id })
          : swt("interface.m0381", { p0: result.intent_id }),
      );
      refreshAfterMutation(deepenFinding);
      setDeepenFinding(null);
      setDeepenDescription("");
    } catch (error) {
      toast.error(swt("interface.m0382", { p0: (error as Error).message }));
    } finally {
      setDeepening(false);
    }
  }

  const statCards = [
    { label: swt("interface.m0383"), value: stats.total, icon: BugIcon },
    {
      label: swt("interface.m0384"),
      value: stats.pending,
      tone: "text-amber-700 dark:text-amber-400",
      icon: ClockIcon,
    },
    { label: swt("interface.m0154"), value: stats.critical, tone: "text-rose-600", icon: ShieldAlertIcon },
    {
      label: swt("interface.m0155"),
      value: stats.high,
      tone: "text-red-600 dark:text-red-400",
      icon: TriangleAlertIcon,
    },
    {
      label: swt("interface.m0156"),
      value: stats.medium,
      tone: "text-amber-700 dark:text-amber-400",
      icon: TriangleAlertIcon,
    },
    { label: swt("interface.m0157"), value: stats.low, tone: "text-slate-500", icon: InfoIcon },
  ];

  // Export current-filter counts use the same filters across views, with different aggregate sources.
  // Flat and asset views share flat-list state; grouped counts come from finding_total.
  const filteredTotal = view === "grouped" ? total : flat.total;
  const assetPath = React.useMemo(
    () => (view === "asset" ? assetPathOf(assetTree.nodes, assetScope) : []),
    [swLocale, assetScope, assetTree.nodes, view],
  );

  const rowProps = {
    selectedIds,
    onToggleSelected: toggleSelected,
    onToggleSelectedPage: toggleSelectedPage,
    expandedKey: expanded,
    onToggleRow: toggleRow,
    reports,
    edit,
    onEditChange: setEdit,
    saving,
    onSave: saveEdit,
    onStatusChange: updateStatus,
    onRetest: setRetestFinding,
    activeRetests,
    onDeepen: openDeepen,
    onDelete: deleteFinding,
  };

  const hasActiveFilters =
    severity !== "all" ||
    status !== "all" ||
    vulnclass !== "all" ||
    task !== "all" ||
    query !== "" ||
    (view === "asset" && assetScope !== null);

  function clearFilters() {
    setSeverity("all");
    setStatus("all");
    setVulnclass("all");
    setTask("all");
    setSearch("");
    setQuery("");
    setAssetScope(null);
  }

  // The list is empty either because nothing exists yet or because the filters exclude everything.
  const emptyFindings = (
    <div className="flex flex-col items-center">
      <EmptyState
        icon={BugIcon}
        title={hasActiveFilters ? swt("states.findingsEmptyFiltered") : swt("states.findingsEmptyAll")}
      />
      <div className="-mt-4 pb-8">
        {hasActiveFilters ? (
          <Button size="sm" variant="outline" onClick={clearFilters}>
            {swt("states.clearFilters")}
          </Button>
        ) : (
          <Button asChild size="sm" variant="outline">
            <Link href="/function/tasks">{swt("states.goToTasks")}</Link>
          </Button>
        )}
      </div>
    </div>
  );

  // The failed query stays on screen as an error until a request succeeds, so rows of another query never reappear.
  function retryFlat() {
    void loadFlat();
  }

  // Flat and right-side asset views share the table and pagination, differing only in filters.
  const flatListCard = (
    <Card className="gap-0 py-0">
      <CardContent className="px-0">
        {flat.error ? (
          <ErrorState title={swt("states.findingsLoadFailed")} detail={flat.error} onRetry={retryFlat} />
        ) : flat.loading && !flat.loaded ? (
          <div className="flex min-h-36 items-center justify-center">
            <Spinner />
          </div>
        ) : flat.loaded && flat.total === 0 && flat.items.length === 0 ? (
          emptyFindings
        ) : (
          <>
            <FindingsTable items={flat.items} selectAllLabel={swt("interface.m0320")} {...rowProps} />
            <TablePagination
              page={flatPage}
              pageSize={flatPageSize}
              total={flat.total}
              onPageChange={setFlatPage}
              onPageSizeChange={(nextSize) => {
                setFlatPageSize(nextSize);
                setFlatPage(1);
              }}
              pageSizeOptions={[10, 20, 50, 100]}
            />
          </>
        )}
      </CardContent>
    </Card>
  );

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <PageHeader
        title={swt("interface.m0188")}
        description={swt("interface.m0385")}
        actions={
          <>
            {selectedIds.size > 0 && (
              <span className="text-xs text-muted-foreground tabular-nums">
                {swt("interface.m0398")} {selectedIds.size} {swt("interface.m0328")}
              </span>
            )}
            <Button size="sm" variant="outline" onClick={openExport}>
              <DownloadIcon /> {swt("interface.m0399")}
            </Button>
          </>
        }
      />
      <div className="flex flex-1 flex-col gap-4 md:gap-6">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
          {statCards.map((stat) => (
            <KpiCard
              key={stat.label}
              icon={stat.icon}
              label={stat.label}
              value={statsLoaded ? <span className={stat.tone}>{stat.value}</span> : "-"}
            />
          ))}
        </div>
        <Tabs value={view} onValueChange={(v) => setView(v as FindingView)}>
          <TabsList variant="line" className="h-9">
            <TabsTrigger value="flat" aria-controls={undefined}>
              <CountLabel label={swt("interface.m0386")} count={flat.loaded ? flat.total : stats.total} />
            </TabsTrigger>
            <TabsTrigger value="grouped" aria-controls={undefined}>
              <CountLabel label={swt("interface.m0387")} count={groupsLoaded ? groupTotal : undefined} />
            </TabsTrigger>
            <TabsTrigger value="asset" aria-controls={undefined}>
              <CountLabel
                label={swt("interface.m0388")}
                count={assetTree.loaded ? assetTree.findingTotal : undefined}
              />
            </TabsTrigger>
          </TabsList>
        </Tabs>

        <div className="flex flex-wrap items-center gap-2">
          <InputGroup className="w-full sm:w-60">
            <InputGroupInput
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={swt("interface.m0389")}
              aria-label={swt("interface.m0389")}
            />
            <InputGroupAddon>
              <SearchIcon aria-hidden="true" />
            </InputGroupAddon>
          </InputGroup>

          <ToggleGroup
            type="single"
            value={severity}
            onValueChange={(value) => value && setSeverity(value as "all" | Severity)}
            variant="outline"
            size="sm"
            spacing={0}
          >
            {(
              [
                ["all", swt("interface.m0172")],
                ["critical", swt("interface.m0154")],
                ["high", swt("interface.m0155")],
                ["medium", swt("interface.m0156")],
                ["low", swt("interface.m0157")],
              ] as const
            ).map(([val, label]) => (
              <ToggleGroupItem key={val} value={val} aria-label={swt("interface.m0390", { p0: label })}>
                {label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>

          <Select value={status} onValueChange={(v) => setStatus(v as "all" | FindingStatus)}>
            <SelectTrigger size="sm" className="w-32">
              <SelectValue placeholder={swt("interface.m0191")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{swt("interface.m0391")}</SelectItem>
              {FINDING_STATUSES.map((st) => (
                <SelectItem key={st} value={st}>
                  {statusMeta("finding", st).label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select value={vulnclass} onValueChange={setVulnclass}>
            <SelectTrigger size="sm" className="w-36">
              <SelectValue placeholder={swt("interface.m0367")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{swt("interface.m0392")}</SelectItem>
              {stats.vulnclasses.map((vc) => (
                <SelectItem key={vc} value={vc}>
                  {vc}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <Select value={task} onValueChange={setTask}>
            <SelectTrigger size="sm" className="w-44">
              <SelectValue placeholder={swt("interface.m0190")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{swt("interface.m0393")}</SelectItem>
              <SelectItem value={UNASSIGNED_TASK}>{swt("interface.m0394")}</SelectItem>
              {(stats.tasks ?? []).map((t) => {
                const id = String(t.id);
                const label = t.name || t.description || swt("interface.m0395", { p0: id });
                return (
                  <SelectItem key={id} value={id}>
                    <span className="flex w-full items-center gap-2">
                      <span className="max-w-[14rem] truncate" title={label}>
                        {label}
                      </span>
                      <span className="inline-flex items-center gap-1 text-muted-foreground tabular-nums">
                        <BugIcon className="size-3.5" aria-hidden="true" />
                        {t.count}
                      </span>
                    </span>
                  </SelectItem>
                );
              })}
            </SelectContent>
          </Select>

          <Select value={sort} onValueChange={(v) => setSort(v as "severity" | "time")}>
            <SelectTrigger size="sm" className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="severity">{swt("interface.m0396")}</SelectItem>
              <SelectItem value="time">{swt("interface.m0397")}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {view === "flat" && flatListCard}

        {view === "asset" && (
          <div className="grid min-h-0 items-start gap-4 lg:grid-cols-[20rem_minmax(0,1fr)] xl:grid-cols-[24rem_minmax(0,1fr)]">
            <Card className="gap-0 py-3 lg:sticky lg:top-4">
              <CardContent className="flex flex-col px-3">
                {assetTree.error ? (
                  <ErrorState
                    title={swt("states.findingsLoadFailed")}
                    detail={assetTree.error}
                    onRetry={() => void loadAssetTree()}
                  />
                ) : assetTree.loading && !assetTree.loaded ? (
                  <div className="flex min-h-36 items-center justify-center">
                    <Spinner />
                  </div>
                ) : (
                  <AssetTree
                    nodes={assetTree.nodes}
                    selected={assetScope}
                    onSelect={setAssetScope}
                    loading={assetTree.loading}
                    truncated={assetTree.truncated}
                    droppedKinds={assetTree.droppedKinds}
                    findingTotal={assetTree.findingTotal}
                    onRefresh={() => void loadAssetTree()}
                  />
                )}
              </CardContent>
            </Card>
            <div className="flex min-w-0 flex-col gap-2">
              <div className="flex min-w-0 flex-wrap items-center gap-1 text-sm text-muted-foreground">
                <button
                  type="button"
                  className={cn("hover:text-foreground", assetScope === null && "font-medium text-foreground")}
                  onClick={() => setAssetScope(null)}
                >
                  {swt("interface.m0312")}
                </button>
                {assetPath.map((node) => (
                  <React.Fragment key={node.key}>
                    <ChevronRightIcon className="size-3.5 shrink-0" aria-hidden="true" />
                    <button
                      type="button"
                      className={cn(
                        "max-w-[16rem] truncate hover:text-foreground",
                        node.key === assetScope && "font-medium text-foreground",
                      )}
                      title={node.label}
                      onClick={() => setAssetScope(node.key)}
                    >
                      {node.display}
                    </button>
                  </React.Fragment>
                ))}
                <span className="ml-auto shrink-0 text-xs tabular-nums">
                  {swt("interface.m0199")} {flat.total} {swt("interface.m0328")}
                </span>
              </div>
              {flatListCard}
            </div>
          </div>
        )}

        {view === "grouped" && (
          <div className="flex flex-col gap-3">
            {(groupsError ? [] : groups).map((group) => {
              const key = findingGroupKey(group);
              const groupOpen = expandedGroups.has(key);
              const state = groupFindings[key] ?? {
                items: [],
                total: group.count,
                page: 1,
                pageSize: 10,
                loaded: false,
                loading: false,
                error: null,
              };
              return (
                <Card key={key} className="gap-0 py-0">
                  <CardHeader className="px-4 py-3">
                    <div className="flex min-w-0 flex-wrap items-center gap-3">
                      <button
                        type="button"
                        className="flex min-w-0 flex-1 items-center gap-3 text-left"
                        aria-expanded={groupOpen}
                        onClick={() => toggleGroup(key)}
                      >
                        <ChevronRightIcon
                          className={cn(
                            "size-4 shrink-0 text-muted-foreground transition-transform",
                            groupOpen && "rotate-90",
                          )}
                        />
                        <div className="flex min-w-0 flex-col gap-1">
                          <CardTitle className="truncate text-sm">
                            {group.task_id === null
                              ? swt("interface.m0394")
                              : group.task_name
                                ? swt("interface.m0400", { p0: group.task_name, p1: group.task_id })
                                : swt("interface.m0401", { p0: group.task_id })}
                          </CardTitle>
                          <CardDescription className="truncate" title={group.task_description}>
                            {group.task_description || swt("interface.m0402")}
                          </CardDescription>
                        </div>
                      </button>
                      <div className="flex flex-wrap items-center gap-2">
                        {group.task_status && <StatusBadge domain="task" value={group.task_status} dot />}
                        {SEVERITIES.map((level) => {
                          const count = group[level];
                          if (count === 0) return null;
                          return (
                            <span key={level} className="inline-flex items-center gap-1">
                              <StatusBadge domain="severity" value={level} dot />
                              <span className="text-xs tabular-nums text-muted-foreground">{count}</span>
                            </span>
                          );
                        })}
                        <span className="text-xs tabular-nums text-muted-foreground">
                          {fmtTime(group.last_found_at)}
                        </span>
                        {group.task_id !== null && (
                          <Button size="icon-sm" variant="ghost" asChild>
                            <Link
                              href={`/function/tasks/detail?id=${group.task_id}`}
                              aria-label={swt("interface.m0403", { p0: group.task_id })}
                            >
                              <ArrowUpRightIcon />
                            </Link>
                          </Button>
                        )}
                      </div>
                    </div>
                  </CardHeader>
                  {groupOpen && (
                    <CardContent className="px-0">
                      {state.loading && !state.loaded ? (
                        <div className="flex min-h-36 items-center justify-center">
                          <Spinner />
                        </div>
                      ) : state.error ? (
                        <ErrorState
                          title={swt("states.findingsLoadFailed")}
                          detail={state.error.message}
                          onRetry={() => {
                            const failed = state.error;
                            if (failed) void loadGroup(key, failed.page, failed.pageSize);
                          }}
                        />
                      ) : (
                        <>
                          <FindingsTable items={state.items} selectAllLabel={swt("interface.m0404")} {...rowProps} />
                          <TablePagination
                            page={state.page}
                            pageSize={state.pageSize}
                            total={state.total}
                            onPageChange={(nextPage) => void loadGroup(key, nextPage, state.pageSize)}
                            onPageSizeChange={(nextSize) => void loadGroup(key, 1, nextSize)}
                          />
                        </>
                      )}
                    </CardContent>
                  )}
                </Card>
              );
            })}
            {(groups.length === 0 || groupsError) && (
              <Card className="gap-0 py-0">
                <CardContent className="px-0">
                  {groupsError ? (
                    <ErrorState
                      title={swt("states.findingsLoadFailed")}
                      detail={groupsError}
                      onRetry={() => void refreshGroups()}
                    />
                  ) : groupsLoaded ? (
                    emptyFindings
                  ) : (
                    <div className="flex min-h-36 items-center justify-center">
                      <Spinner />
                    </div>
                  )}
                </CardContent>
              </Card>
            )}
            {!groupsError && (
              <TablePagination
                page={page}
                pageSize={pageSize}
                total={groupTotal}
                onPageChange={setPage}
                onPageSizeChange={(nextPageSize) => {
                  setPageSize(nextPageSize);
                  setPage(1);
                }}
                pageSizeOptions={[5, 10, 20]}
              />
            )}
          </div>
        )}
      </div>

      {retestFinding?.finding_id ? (
        <FindingRetestDialog
          key={retestFinding.finding_id}
          findingId={retestFinding.finding_id}
          findingName={retestFinding.name || retestFinding.vulnclass || retestFinding.summary}
          onStarted={(retest) => {
            const findingId = retestFinding.finding_id;
            if (!findingId || retest.conversation_id == null || !["pending", "running"].includes(retest.status)) return;
            retestRefreshVersion.current++;
            const active: ActiveFindingRetest = {
              id: retest.id,
              finding_id: findingId,
              conversation_id: retest.conversation_id,
              status: retest.status === "pending" ? "pending" : "running",
            };
            setActiveRetests((current) => ({ ...current, [findingId]: active }));
          }}
          onClose={() => setRetestFinding(null)}
        />
      ) : null}

      <Dialog
        open={deepenFinding !== null}
        onOpenChange={(open) => {
          if (open || deepening) return;
          setDeepenFinding(null);
          setDeepenDescription("");
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{swt("interface.m0405")}</DialogTitle>
            <DialogDescription className="break-words">
              {swt("interface.m0406")}
              {deepenFinding?.task_id} {swt("interface.m0407")}
              {deepenFinding?.name || deepenFinding?.vulnclass || deepenFinding?.summary}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="finding-deepen-description">{swt("interface.m0408")}</FieldLabel>
              <Textarea
                id="finding-deepen-description"
                value={deepenDescription}
                onChange={(event) => setDeepenDescription(event.target.value)}
                maxLength={4000}
                placeholder={swt("interface.m0409")}
                disabled={deepening}
              />
              <FieldDescription className="flex justify-between gap-3">
                <span>{swt("interface.m0410")}</span>
                <span className="shrink-0 tabular-nums">{deepenDescription.length} / 4000</span>
              </FieldDescription>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setDeepenFinding(null);
                setDeepenDescription("");
              }}
              disabled={deepening}
            >
              {swt("interface.m0063")}
            </Button>
            <Button onClick={submitDeepen} disabled={deepening || !deepenDescription.trim()}>
              {deepening && <Spinner data-icon="inline-start" />}
              {swt("interface.m0411")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={exportOpen} onOpenChange={setExportOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{swt("interface.m0412")}</DialogTitle>
            <DialogDescription>{swt("interface.m0413")}</DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-5 py-1">
            <div className="flex flex-col gap-2">
              <span className="text-xs text-muted-foreground">{swt("interface.m0414")}</span>
              <RadioGroup value={exportScope} onValueChange={(v) => setExportScope(v as typeof exportScope)}>
                <label htmlFor="export-scope-filtered" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-scope-filtered" value="filtered" /> {swt("interface.m0415")}{" "}
                  {filteredTotal} {swt("interface.m0416")}
                </label>
                <label htmlFor="export-scope-all" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-scope-all" value="all" /> {swt("interface.m0417")}
                </label>
                <label
                  htmlFor="export-scope-selected"
                  className={cn("flex items-center gap-2 text-sm", selectedIds.size === 0 && "text-muted-foreground")}
                >
                  <RadioGroupItem id="export-scope-selected" value="selected" disabled={selectedIds.size === 0} />
                  {swt("interface.m0418")} {selectedIds.size} {swt("interface.m0328")}
                </label>
              </RadioGroup>
            </div>

            <div className="flex flex-col gap-2">
              <span className="text-xs text-muted-foreground">{swt("interface.m0419")}</span>
              <RadioGroup value={exportFormat} onValueChange={(v) => setExportFormat(v as typeof exportFormat)}>
                <label htmlFor="export-format-md-single" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-md-single" value="md-single" /> {swt("interface.m0420")}
                </label>
                <label htmlFor="export-format-md-zip" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-md-zip" value="md-zip" /> {swt("interface.m0421")}
                </label>
                <label htmlFor="export-format-csv" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-csv" value="csv" /> {swt("interface.m0422")}
                </label>
                <label htmlFor="export-format-json" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-json" value="json" /> JSON (.json)
                </label>
                <label htmlFor="export-format-pdf" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-pdf" value="pdf" /> PDF report (.pdf, credentials redacted)
                </label>
                <label htmlFor="export-format-sarif" className="flex items-center gap-2 text-sm">
                  <RadioGroupItem id="export-format-sarif" value="sarif" /> {swt("report.exportSarifFormat")}
                </label>
                {exportFormat === "sarif" && (
                  <p className="pl-6 text-muted-foreground text-xs">{swt("report.exportSarifHint")}</p>
                )}
              </RadioGroup>
            </div>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setExportOpen(false)} disabled={exporting}>
              {swt("interface.m0063")}
            </Button>
            <Button onClick={doExport} disabled={exporting || (exportScope === "selected" && selectedIds.size === 0)}>
              <DownloadIcon /> {exporting ? swt("interface.m0423") : swt("interface.m0399")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
