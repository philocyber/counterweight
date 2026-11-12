"use client";

import * as React from "react";

import { KeyRoundIcon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useI18n } from "@/i18n";
import { getIntlLocale } from "@/i18n/runtime";
import { api } from "@/lib/api";
import type { GitHubTokenStatus } from "@/lib/types";

/**
 * GitHub token used to open pull requests with verified fixes. Entered once and
 * reused until it expires or is replaced; the server never returns the token.
 */
export function GitHubTokenCard({
  className,
  onChange,
}: {
  className?: string;
  onChange?: (status: GitHubTokenStatus) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [confirm, confirmDialog] = useConfirm();
  const [status, setStatus] = React.useState<GitHubTokenStatus | null>(null);
  const [token, setToken] = React.useState("");
  const [editing, setEditing] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  const apply = React.useCallback(
    (next: GitHubTokenStatus) => {
      setStatus(next);
      onChange?.(next);
    },
    [onChange],
  );

  React.useEffect(() => {
    api
      .githubToken()
      .then(apply)
      .catch(() => apply({ configured: false, expired: false }));
  }, [apply]);

  const save = async () => {
    setBusy(true);
    try {
      apply(await api.saveGitHubToken(token));
      setToken("");
      setEditing(false);
      toast.success(swt("interface.githubTokenSaved"));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    const ok = await confirm({
      title: swt("confirm.removeGithubTokenTitle"),
      description: swt("confirm.removeGithubTokenDescription"),
      confirmLabel: swt("confirm.remove"),
      destructive: true,
    });
    if (!ok) return;
    setBusy(true);
    try {
      apply(await api.deleteGitHubToken());
      toast.success(swt("interface.githubTokenRemoved"));
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const expires = status?.expires_at
    ? swt("interface.githubTokenExpires", {
        p0: new Date(status.expires_at).toLocaleDateString(getIntlLocale()),
      })
    : swt("interface.githubTokenNoExpiry");
  const showForm = !status?.configured || status.expired || editing;

  return (
    <Card className={className}>
      {confirmDialog}
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <KeyRoundIcon className="size-4" />
          {swt("interface.githubTokenTitle")}
        </CardTitle>
        <CardDescription>{swt("interface.githubTokenDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {status?.configured ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Badge variant={status.expired ? "destructive" : "secondary"}>
              {swt("interface.githubTokenConnected", { p0: status.login ?? "" })}
            </Badge>
            <span className="text-muted-foreground">
              {status.hint} · {expires}
            </span>
            {status.expired ? <span className="text-destructive">{swt("interface.githubTokenExpired")}</span> : null}
            <div className="ml-auto flex gap-2">
              {!editing ? (
                <Button variant="outline" size="sm" disabled={busy} onClick={() => setEditing(true)}>
                  {swt("interface.githubTokenReplace")}
                </Button>
              ) : null}
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => void remove()}>
                {swt("interface.githubTokenRemove")}
              </Button>
            </div>
          </div>
        ) : null}
        {showForm ? (
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (token.trim()) void save();
            }}
          >
            <Input
              type="password"
              autoComplete="off"
              spellCheck={false}
              placeholder="github_pat_…"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              aria-label={swt("interface.githubTokenTitle")}
            />
            <Button type="submit" disabled={busy || !token.trim()}>
              {swt("interface.githubTokenSave")}
            </Button>
          </form>
        ) : null}
      </CardContent>
    </Card>
  );
}
