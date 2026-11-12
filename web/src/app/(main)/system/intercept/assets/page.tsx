"use client";

import * as React from "react";

import { BanIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { type MessageKey, useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { AssetInterceptKind, AssetInterceptRule } from "@/lib/types";

// Kind metadata.

const KIND_OPTIONS: { value: AssetInterceptKind; label: string; group: string; placeholder: string }[] = [
  {
    value: "exact_domain",
    get label() {
      return swt("interface.m1151");
    },
    get group() {
      return swt("interface.m1152");
    },
    placeholder: "example.gov",
  },
  {
    value: "exact_ip",
    get label() {
      return swt("interface.m1153");
    },
    get group() {
      return swt("interface.m1152");
    },
    placeholder: "203.0.113.10",
  },
  {
    value: "exact_url",
    get label() {
      return swt("interface.m1154");
    },
    get group() {
      return swt("interface.m1152");
    },
    placeholder: "https://example.gov/login",
  },
  {
    value: "fuzzy_domain",
    get label() {
      return swt("interface.m1155");
    },
    get group() {
      return swt("interface.m1156");
    },
    placeholder: ".gov",
  },
  {
    value: "fuzzy_ip",
    get label() {
      return swt("interface.m1157");
    },
    get group() {
      return swt("interface.m1156");
    },
    placeholder: "203.0.113.",
  },
  {
    value: "fuzzy_url",
    get label() {
      return swt("interface.m1158");
    },
    get group() {
      return swt("interface.m1156");
    },
    placeholder: "/admin",
  },
  {
    value: "cidr",
    get label() {
      return swt("interface.m0670");
    },
    get group() {
      return swt("interface.m0598");
    },
    placeholder: "192.168.0.0/16",
  },
];

const KIND_LABEL: Record<AssetInterceptKind, string> = Object.fromEntries(
  KIND_OPTIONS.map((o) => [o.value, o.label]),
) as Record<AssetInterceptKind, string>;

const KIND_GROUPS = [swt("interface.m1152"), swt("interface.m1156"), swt("interface.m0598")];

// Ready-made rules to start from. The addresses are documentation examples (example.com,
// 203.0.113.0/24), so a rule created from one blocks nothing real until it is changed.
const EXAMPLES: { want: MessageKey; note: MessageKey; kind: AssetInterceptKind; pattern: string }[] = [
  { want: "blocked.oneHost", note: "blocked.oneHostNote", kind: "exact_domain", pattern: "billing.example.com" },
  { want: "blocked.subdomains", note: "blocked.subdomainsNote", kind: "fuzzy_domain", pattern: ".example.com" },
  { want: "blocked.wholeDomain", note: "blocked.wholeDomainNote", kind: "fuzzy_domain", pattern: "example.com" },
  { want: "blocked.environment", note: "blocked.environmentNote", kind: "fuzzy_domain", pattern: ".prod." },
  { want: "blocked.topLevel", note: "blocked.topLevelNote", kind: "fuzzy_domain", pattern: ".gov" },
  {
    want: "blocked.oneUrl",
    note: "blocked.oneUrlNote",
    kind: "exact_url",
    pattern: "https://app.example.com/account/close",
  },
  { want: "blocked.underPath", note: "blocked.underPathNote", kind: "fuzzy_url", pattern: "app.example.com/billing/" },
  { want: "blocked.anyHostPath", note: "blocked.anyHostPathNote", kind: "fuzzy_url", pattern: "/logout" },
  { want: "blocked.oneIp", note: "blocked.oneIpNote", kind: "exact_ip", pattern: "203.0.113.10" },
  { want: "blocked.ipRange", note: "blocked.ipRangeNote", kind: "cidr", pattern: "203.0.113.0/24" },
  { want: "blocked.ipPrefix", note: "blocked.ipPrefixNote", kind: "fuzzy_ip", pattern: "203.0.113." },
];

function KindBadge({ kind }: { kind: AssetInterceptKind }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const fuzzy = kind.startsWith("fuzzy_");
  const cidr = kind === "cidr";
  return (
    <Badge
      variant="outline"
      className={
        cidr
          ? "border-sky-400 text-sky-700 dark:text-sky-400"
          : fuzzy
            ? "border-amber-400 text-amber-700 dark:text-amber-400"
            : "border-emerald-400 text-emerald-700 dark:text-emerald-400"
      }
    >
      {KIND_LABEL[kind]}
    </Badge>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  return (
    <div className="space-y-1.5">
      <Label className="text-xs font-medium text-muted-foreground uppercase tracking-wide">{label}</Label>
      {children}
      {hint && <p className="text-[11px] text-muted-foreground">{hint}</p>}
    </div>
  );
}

// ---- form state ----

type RuleForm = {
  enabled: boolean;
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
};

const defaultForm = (): RuleForm => ({ enabled: true, kind: "fuzzy_domain", pattern: "", note: "" });

// Light validation mirrors backend: validate only exact_ip/cidr formats; leave other checks to the server.
function frontValidate(form: RuleForm): string | null {
  const p = form.pattern.trim();
  if (!p) return swt("interface.m1159");
  if (form.kind === "cidr" && !/^[0-9a-fA-F:.]+\/\d{1,3}$/.test(p)) {
    return swt("interface.m1160");
  }
  return null;
}

// ---- page ----

export default function AssetInterceptPage() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();
  const [confirm, confirmDialog] = useConfirm();

  const [rules, setRules] = React.useState<AssetInterceptRule[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<AssetInterceptRule | null>(null);
  const [form, setForm] = React.useState<RuleForm>(defaultForm());
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      setRules(await api.assetInterceptRules());
    } catch {
      toast.error(swt("interface.m1161"));
    } finally {
      setLoading(false);
    }
  }, [swLocale]);

  React.useEffect(() => {
    load();
  }, [load]);

  function set(patch: Partial<RuleForm>) {
    setForm((f) => ({ ...f, ...patch }));
  }

  function openNew() {
    setEditing(null);
    setForm(defaultForm());
    setOpen(true);
  }

  // Opens the editor filled from an example; nothing is saved until the user saves it.
  function openExample(example: (typeof EXAMPLES)[number]) {
    setEditing(null);
    setForm({ enabled: true, kind: example.kind, pattern: example.pattern, note: swt(example.note) });
    setOpen(true);
  }

  function openEdit(rule: AssetInterceptRule) {
    setEditing(rule);
    setForm({ enabled: rule.enabled, kind: rule.kind, pattern: rule.pattern, note: rule.note });
    setOpen(true);
  }

  async function handleSave() {
    const err = frontValidate(form);
    if (err) {
      toast.error(err);
      return;
    }
    const payload = { ...form, pattern: form.pattern.trim() };
    setSaving(true);
    try {
      if (editing) {
        await api.updateAssetInterceptRule(editing.id, payload);
        toast.success(swt("interface.m1162"));
      } else {
        await api.createAssetInterceptRule(payload);
        toast.success(swt("interface.m1163"));
      }
      setOpen(false);
      load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(rule: AssetInterceptRule) {
    const ok = await confirm({
      title: swt("interface.m1164", { p0: rule.pattern }),
      description: swt("confirm.deleteAssetRuleDescription"),
      confirmLabel: swt("confirm.delete"),
      destructive: true,
    });
    if (!ok) return;
    try {
      await api.deleteAssetInterceptRule(rule.id);
      toast.success(swt("interface.m1165"));
      load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  async function handleToggle(rule: AssetInterceptRule) {
    try {
      await api.toggleAssetInterceptRule(rule.id, !rule.enabled);
      load();
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  const placeholder = KIND_OPTIONS.find((o) => o.value === form.kind)?.placeholder ?? "";

  return (
    <div className="flex flex-1 flex-col gap-5">
      {confirmDialog}
      <PageHeader title={swt("nav.assetIntercept")} description={swt("interface.m1167")} />

      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">{swt("interface.m1168")}</p>
        <Button onClick={openNew} size="sm" className="shrink-0">
          <PlusIcon className="h-4 w-4" />
          {swt("interface.m1169")}
        </Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {loading ? (
            <p className="p-6 text-sm text-muted-foreground">{swt("interface.m0260")}</p>
          ) : rules.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
              <BanIcon className="h-8 w-8 text-muted-foreground/40" />
              <p className="text-sm text-muted-foreground">{swt("interface.m1170")}</p>
              <Button size="sm" variant="outline" onClick={openNew}>
                <PlusIcon className="h-4 w-4" />
                {swt("interface.m1171")}
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-[130px]">{swt("interface.m0546")}</TableHead>
                  <TableHead>{swt("interface.m1172")}</TableHead>
                  <TableHead>{swt("interface.m1173")}</TableHead>
                  <TableHead className="w-[64px] text-center">{swt("interface.m1174")}</TableHead>
                  <TableHead className="w-[80px]" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rules.map((rule) => (
                  <TableRow key={rule.id} className={!rule.enabled ? "opacity-40" : ""}>
                    <TableCell>
                      <KindBadge kind={rule.kind} />
                    </TableCell>
                    <TableCell className="max-w-[280px]">
                      <code className="block truncate rounded bg-muted px-1.5 py-0.5 text-xs font-mono">
                        {rule.pattern}
                      </code>
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      <div className="flex items-center gap-1.5">
                        {rule.builtin && (
                          <Badge variant="secondary" className="shrink-0 px-1 py-0 text-[10px]">
                            {swt("interface.m1135")}
                          </Badge>
                        )}
                        <span className="truncate">{rule.note}</span>
                      </div>
                    </TableCell>
                    <TableCell className="text-center">
                      <Switch checked={rule.enabled} onCheckedChange={() => handleToggle(rule)} />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-0.5">
                        <Button size="icon" variant="ghost" className="h-7 w-7" onClick={() => openEdit(rule)}>
                          <PencilIcon className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-7 w-7 text-destructive hover:text-destructive"
                          onClick={() => handleDelete(rule)}
                        >
                          <Trash2Icon className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{swt("blocked.examplesTitle")}</CardTitle>
          <CardDescription>{swt("blocked.examplesDescription")}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>{swt("blocked.colWant")}</TableHead>
                  <TableHead className="w-[150px]">{swt("blocked.colType")}</TableHead>
                  <TableHead>{swt("blocked.colPattern")}</TableHead>
                  <TableHead className="w-[80px]" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {EXAMPLES.map((example) => (
                  <TableRow key={example.want}>
                    <TableCell className="text-sm">{swt(example.want)}</TableCell>
                    <TableCell>
                      <KindBadge kind={example.kind} />
                    </TableCell>
                    <TableCell>
                      <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{example.pattern}</code>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="outline"
                        aria-label={swt("blocked.useAria", { p0: swt(example.want) })}
                        onClick={() => openExample(example)}
                      >
                        {swt("blocked.use")}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <p className="text-muted-foreground text-xs">{swt("blocked.footnote")}</p>
        </CardContent>
      </Card>

      {/* editor sheet */}
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="right" className="flex flex-col gap-0 p-0 sm:max-w-md">
          <SheetHeader className="border-b px-6 py-4">
            <SheetTitle>{editing ? swt("interface.m1175") : swt("interface.m1176")}</SheetTitle>
            <SheetDescription className="text-xs">{swt("interface.m1177")}</SheetDescription>
          </SheetHeader>

          <div className="flex-1 min-h-0 overflow-y-auto px-6 py-5 space-y-5">
            <Field label={swt("interface.m1178")}>
              <Select value={form.kind} onValueChange={(v) => set({ kind: v as AssetInterceptKind })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {KIND_GROUPS.map((g) => (
                    <React.Fragment key={g}>
                      <div className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                        {g}
                      </div>
                      {KIND_OPTIONS.filter((o) => o.group === g).map((o) => (
                        <SelectItem key={o.value} value={o.value}>
                          {o.label}
                        </SelectItem>
                      ))}
                    </React.Fragment>
                  ))}
                </SelectContent>
              </Select>
            </Field>

            <Field
              label={swt("interface.m1172")}
              hint={
                form.kind === "cidr"
                  ? swt("interface.m1179")
                  : form.kind.startsWith("fuzzy_")
                    ? swt("interface.m1180")
                    : swt("interface.m1181")
              }
            >
              <Input
                placeholder={placeholder}
                value={form.pattern}
                onChange={(e) => set({ pattern: e.target.value })}
              />
            </Field>

            <Field label={swt("interface.m1182")}>
              <Textarea
                placeholder={swt("interface.m1183")}
                value={form.note}
                onChange={(e) => set({ note: e.target.value })}
                rows={2}
                className="resize-none"
              />
            </Field>

            <Separator />

            <div className="flex items-center gap-3">
              <Switch id="asset-rule-enabled" checked={form.enabled} onCheckedChange={(v) => set({ enabled: v })} />
              <Label htmlFor="asset-rule-enabled" className="cursor-pointer">
                {swt("interface.m1184")}
              </Label>
            </div>
          </div>

          <SheetFooter className="border-t px-6 py-4 flex-row justify-end gap-2">
            <Button variant="outline" onClick={() => setOpen(false)}>
              {swt("interface.m0063")}
            </Button>
            <Button onClick={handleSave} disabled={saving}>
              {saving ? swt("interface.m0272") : swt("interface.m0273")}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
    </div>
  );
}
