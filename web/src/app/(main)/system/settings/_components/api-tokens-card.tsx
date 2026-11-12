"use client";

import * as React from "react";

import Link from "next/link";

import { CheckIcon, CopyIcon, KeyRoundIcon, PlusIcon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { type MessageKey, useI18n } from "@/i18n";
import { getIntlLocale } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { ApiToken } from "@/lib/types";

const SCOPES: { value: string; label: MessageKey; hint: MessageKey }[] = [
  { value: "reviews:write", label: "tokens.scopeWrite", hint: "tokens.scopeWriteHint" },
  { value: "reviews:read", label: "tokens.scopeRead", hint: "tokens.scopeReadHint" },
];

type TokenState = "active" | "revoked" | "expired";

const STATE_LABELS: Record<TokenState, MessageKey> = {
  active: "tokens.stateActive",
  revoked: "tokens.stateRevoked",
  expired: "tokens.stateExpired",
};

function tokenState(token: ApiToken, now: number): TokenState {
  if (token.revoked_at) return "revoked";
  if (token.expires_at && Date.parse(token.expires_at) <= now) return "expired";
  return "active";
}

// The end of the chosen local day, as an RFC 3339 instant; null when the date is empty or in the past.
function expiryInstant(date: string, now: number): { iso: string | null; past: boolean } {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!match) return { iso: null, past: false };
  const end = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), 23, 59, 59);
  if (Number.isNaN(end.getTime())) return { iso: null, past: false };
  return end.getTime() <= now ? { iso: null, past: true } : { iso: end.toISOString(), past: false };
}

function CreateTokenDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (secret: string) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [name, setName] = React.useState("");
  const [scopes, setScopes] = React.useState<string[]>(["reviews:write", "reviews:read"]);
  const [expires, setExpires] = React.useState("");
  const [touched, setTouched] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    if (!open) return;
    setName("");
    setScopes(["reviews:write", "reviews:read"]);
    setExpires("");
    setTouched(false);
    setError("");
  }, [open]);

  const expiry = expiryInstant(expires, Date.now());
  const nameMissing = name.trim() === "";
  const scopesMissing = scopes.length === 0;
  const invalid = nameMissing || scopesMissing || expiry.past;

  async function submit() {
    setTouched(true);
    if (busy || invalid) return;
    setBusy(true);
    setError("");
    try {
      const created = await api.createApiToken({
        name: name.trim(),
        scopes,
        ...(expiry.iso ? { expires_at: expiry.iso } : {}),
      });
      onCreated(created.token);
    } catch (e) {
      setError(swt("tokens.createFailed", { p0: (e as Error).message }));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{swt("tokens.dialogTitle")}</DialogTitle>
            <DialogDescription>{swt("tokens.dialogDescription")}</DialogDescription>
          </DialogHeader>
          <Field data-invalid={touched && nameMissing}>
            <FieldLabel htmlFor="api-token-name">{swt("tokens.nameLabel")}</FieldLabel>
            <Input
              id="api-token-name"
              value={name}
              maxLength={100}
              disabled={busy}
              autoComplete="off"
              placeholder={swt("tokens.namePlaceholder")}
              aria-invalid={touched && nameMissing}
              onChange={(event) => setName(event.target.value)}
            />
            <FieldDescription>{swt("tokens.nameHint")}</FieldDescription>
            {touched && nameMissing && <FieldError>{swt("tokens.nameRequired")}</FieldError>}
          </Field>
          <FieldSet>
            <FieldLegend variant="label">{swt("tokens.scopesLegend")}</FieldLegend>
            <div className="grid gap-2">
              {SCOPES.map((scope) => {
                const id = `api-token-scope-${scope.value.replace(":", "-")}`;
                return (
                  <label
                    key={scope.value}
                    htmlFor={id}
                    className="flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border p-3 hover:bg-accent/50"
                  >
                    <Checkbox
                      id={id}
                      className="mt-0.5"
                      checked={scopes.includes(scope.value)}
                      disabled={busy}
                      onCheckedChange={(checked) =>
                        setScopes((current) =>
                          checked === true
                            ? [...current.filter((s) => s !== scope.value), scope.value]
                            : current.filter((s) => s !== scope.value),
                        )
                      }
                    />
                    <span className="grid min-w-0 flex-1 gap-0.5">
                      <span className="font-medium text-sm leading-snug">{swt(scope.label)}</span>
                      <span className="text-muted-foreground text-xs leading-snug">{swt(scope.hint)}</span>
                    </span>
                  </label>
                );
              })}
            </div>
            {touched && scopesMissing && <FieldError>{swt("tokens.scopesRequired")}</FieldError>}
          </FieldSet>
          <Field data-invalid={expiry.past}>
            <FieldLabel htmlFor="api-token-expiry">{swt("tokens.expiryLabel")}</FieldLabel>
            <Input
              id="api-token-expiry"
              type="date"
              value={expires}
              disabled={busy}
              aria-invalid={expiry.past}
              className="w-fit max-w-full"
              onChange={(event) => setExpires(event.target.value)}
            />
            <FieldDescription>{swt("tokens.expiryHint")}</FieldDescription>
            {expiry.past && <FieldError>{swt("tokens.expiryPast")}</FieldError>}
          </Field>
          {error && (
            <p role="alert" className="break-words text-destructive text-sm">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {swt("tokens.cancel")}
            </Button>
            <Button type="submit" disabled={busy || (touched && invalid)}>
              {busy && <Spinner data-icon="inline-start" />}
              {busy ? swt("tokens.creating") : swt("tokens.createAction")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// SecretDialog shows the new token once. Closing it drops the secret from memory.
function SecretDialog({ secret, onClose }: { secret: string | null; onClose: () => void }) {
  "use no memo";
  const { t: swt } = useI18n();
  const [copied, setCopied] = React.useState(false);
  React.useEffect(() => {
    if (secret) setCopied(false);
  }, [secret]);

  async function copy() {
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
    } catch {
      toast.error(swt("tokens.copyFailed"));
    }
  }

  return (
    <Dialog open={secret !== null} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{swt("tokens.secretTitle")}</DialogTitle>
          <DialogDescription>{swt("tokens.secretDescription")}</DialogDescription>
        </DialogHeader>
        <Field>
          <FieldLabel htmlFor="api-token-secret">{swt("tokens.secretLabel")}</FieldLabel>
          <div className="flex min-w-0 gap-2">
            <Input
              id="api-token-secret"
              readOnly
              value={secret ?? ""}
              spellCheck={false}
              autoComplete="off"
              className="min-w-0 font-mono text-xs"
              onFocus={(event) => event.currentTarget.select()}
            />
            <Button type="button" variant="outline" className="shrink-0" onClick={() => void copy()}>
              {copied ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
              {copied ? swt("tokens.copied") : swt("tokens.copy")}
            </Button>
          </div>
        </Field>
        <DialogFooter>
          <Button type="button" onClick={onClose}>
            {swt("tokens.done")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TokenRow({ token, busy, onRevoke }: { token: ApiToken; busy: boolean; onRevoke: (token: ApiToken) => void }) {
  "use no memo";
  const { t: swt } = useI18n();
  const state = tokenState(token, Date.now());
  const day = (iso: string) => new Date(iso).toLocaleDateString(getIntlLocale());
  const scopeLabel = (scope: string) => {
    const known = SCOPES.find((s) => s.value === scope);
    return known ? swt(known.label) : scope;
  };
  return (
    <li className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2 border-b px-4 py-3 last:border-b-0">
      <div className="flex min-w-0 flex-1 basis-60 flex-col gap-1.5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="min-w-0 truncate font-medium text-sm" title={token.name}>
            {token.name}
          </span>
          <Badge variant={state === "active" ? "secondary" : "outline"} className="shrink-0">
            {swt(STATE_LABELS[state])}
          </Badge>
          <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{token.hint}</code>
        </div>
        <div className="flex flex-wrap gap-1">
          {token.scopes.map((scope) => (
            <Badge key={scope} variant="outline" className="max-w-full">
              <span className="truncate">{scopeLabel(scope)}</span>
            </Badge>
          ))}
        </div>
        <dl className="flex flex-wrap gap-x-4 gap-y-0.5 text-muted-foreground text-xs">
          <div className="flex gap-1">
            <dt>{swt("tokens.colCreated")}:</dt>
            <dd>{day(token.created_at)}</dd>
          </div>
          <div className="flex gap-1">
            <dt>{swt("tokens.colLastUsed")}:</dt>
            <dd>{token.last_used_at ? day(token.last_used_at) : swt("tokens.neverUsed")}</dd>
          </div>
          <div className="flex gap-1">
            <dt>{swt("tokens.colExpires")}:</dt>
            <dd>{token.expires_at ? day(token.expires_at) : swt("tokens.neverExpires")}</dd>
          </div>
          {token.revoked_at && <div>{swt("tokens.revokedOn", { p0: day(token.revoked_at) })}</div>}
        </dl>
      </div>
      {!token.revoked_at && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy}
          aria-label={swt("tokens.revokeAria", { p0: token.name })}
          onClick={() => onRevoke(token)}
        >
          {swt("tokens.revoke")}
        </Button>
      )}
    </li>
  );
}

// ApiTokensCard manages the tokens a CI workflow uses to start and read pull request reviews.
export function ApiTokensCard() {
  "use no memo";
  const { t: swt } = useI18n();
  const [confirm, confirmDialog] = useConfirm();
  const [tokens, setTokens] = React.useState<ApiToken[] | null>(null);
  const [failed, setFailed] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [secret, setSecret] = React.useState<string | null>(null);
  const [busyId, setBusyId] = React.useState<number | null>(null);

  const load = React.useCallback(async () => {
    try {
      setTokens(await api.apiTokens());
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function revoke(token: ApiToken) {
    const ok = await confirm({
      title: swt("tokens.revokeTitle"),
      description: swt("tokens.revokeDescription"),
      confirmLabel: swt("tokens.revoke"),
      destructive: true,
    });
    if (!ok) return;
    setBusyId(token.id);
    try {
      await api.revokeApiToken(token.id);
      toast.success(swt("tokens.revoked"));
      await load();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusyId(null);
    }
  }

  return (
    <Card>
      {confirmDialog}
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <KeyRoundIcon className="size-4" />
          {swt("tokens.title")}
        </CardTitle>
        <CardDescription>
          {swt("tokens.description")}{" "}
          <Link href="/docs/#pr-reviews" className="font-medium text-primary hover:underline">
            {swt("tokens.docsLink")}
          </Link>
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div>
          <Button type="button" size="sm" onClick={() => setCreating(true)}>
            <PlusIcon data-icon="inline-start" />
            {swt("tokens.create")}
          </Button>
        </div>
        {failed && (
          <div role="alert" className="flex flex-wrap items-center gap-3 text-sm">
            <span>{swt("tokens.loadFailed")}</span>
            <Button type="button" size="sm" variant="outline" onClick={() => void load()}>
              {swt("tokens.retry")}
            </Button>
          </div>
        )}
        {tokens && tokens.length === 0 && !failed && (
          <p className="text-muted-foreground text-sm">{swt("tokens.empty")}</p>
        )}
        {tokens && tokens.length > 0 && (
          <ul className="overflow-hidden rounded-lg border">
            {tokens.map((token) => (
              <TokenRow key={token.id} token={token} busy={busyId === token.id} onRevoke={(t) => void revoke(t)} />
            ))}
          </ul>
        )}
      </CardContent>
      <CreateTokenDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={(value) => {
          setCreating(false);
          setSecret(value);
          void load();
        }}
      />
      <SecretDialog secret={secret} onClose={() => setSecret(null)} />
    </Card>
  );
}
