"use client";

import * as React from "react";

import { ExternalLinkIcon, Loader2Icon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import type { ChatGPTAccount } from "@/lib/types";

export function SubscriptionConnection({ value, onChange }: { value: string; onChange: (id: string) => void }) {
  const { t } = useI18n();
  const [accounts, setAccounts] = React.useState<ChatGPTAccount[]>([]);
  const [busy, setBusy] = React.useState(false);
  const [authUrl, setAuthUrl] = React.useState("");
  const [error, setError] = React.useState("");
  const active = React.useRef<{ id: string; popup: Window; stopped: boolean } | null>(null);
  const mounted = React.useRef(true);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  React.useEffect(() => {
    mounted.current = true;
    void api
      .chatGPTAccounts()
      .then(setAccounts)
      .catch((e: Error) => setError(e.message));
    return () => {
      mounted.current = false;
      if (timer.current) clearTimeout(timer.current);
      const attempt = active.current;
      if (attempt) {
        attempt.stopped = true;
        attempt.popup.close();
        void api.cancelChatGPTLogin(attempt.id).catch(() => {});
      }
    };
  }, []);
  async function cancel() {
    const attempt = active.current;
    if (!attempt) return;
    attempt.stopped = true;
    attempt.popup.close();
    active.current = null;
    if (timer.current) clearTimeout(timer.current);
    setAuthUrl("");
    setBusy(false);
    try {
      await api.cancelChatGPTLogin(attempt.id);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function connect() {
    if (busy) return;
    const popup = window.open("about:blank", "counterweight-chatgpt", "popup,width=640,height=800");
    if (!popup) {
      setError(t("providers.popupBlocked"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const login = await api.startChatGPTLogin(value);
      if (!mounted.current) {
        popup.close();
        await api.cancelChatGPTLogin(login.id);
        return;
      }
      const attempt = { id: login.id, popup, stopped: false };
      active.current = attempt;
      setAuthUrl(login.url ?? "");
      popup.location.href = login.url ?? "about:blank";
      async function poll() {
        if (attempt.stopped) return;
        try {
          const status = await api.chatGPTLoginStatus(attempt.id);
          if (attempt.stopped || !mounted.current) return;
          if (status.status === "connected" && status.account) {
            attempt.stopped = true;
            attempt.popup.close();
            active.current = null;
            setBusy(false);
            setAuthUrl("");
            const account = status.account;
            setAccounts((current) => [...current.filter((a) => a.id !== account.id), account]);
            onChange(account.id);
            toast.success(t("providers.subscriptionConnected"));
            return;
          }
          if (!["pending", "exchanging"].includes(status.status)) {
            if (status.account) {
              const account = status.account;
              setAccounts((current) => [...current.filter((a) => a.id !== account.id), account]);
              onChange(account.id);
            }
            throw new Error(status.error || t("providers.loginCancelled"));
          }
          // OAuth pages may sever the popup handle through COOP. Rely on the
          // callback status, explicit cancellation, and the server timeout.
          timer.current = setTimeout(() => void poll(), 1000);
        } catch (e) {
          if (attempt.stopped || !mounted.current) return;
          setError((e as Error).message);
          await cancel();
        }
      }
      void poll();
    } catch (e) {
      popup.close();
      if (mounted.current) {
        setBusy(false);
        setError((e as Error).message);
      }
    }
  }
  return (
    <div className="grid gap-3 rounded-md border bg-muted/20 p-3">
      <Label>{t("providers.subscriptionAccount")}</Label>
      <Select value={value || "new"} disabled={busy} onValueChange={(id) => onChange(id === "new" ? "" : id)}>
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="new">{t("providers.subscriptionNewAccount")}</SelectItem>
          {accounts.map((account) => (
            <SelectItem key={account.id} value={account.id}>
              {account.email || "ChatGPT"} · {account.id.slice(0, 8)}
              {account.connected ? "" : ` (${t("providers.subscriptionDisconnected")})`}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-muted-foreground text-xs">{t("providers.subscriptionHelp")}</p>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" disabled={busy} onClick={() => void connect()}>
          {busy ? <Loader2Icon className="animate-spin" /> : <ExternalLinkIcon />}
          {busy ? t("providers.subscriptionWaiting") : t("providers.continueChatGPT")}
        </Button>
        {busy && (
          <Button type="button" variant="ghost" onClick={() => void cancel()}>
            {t("providers.subscriptionCancel")}
          </Button>
        )}
        {busy && authUrl && (
          <a href={authUrl} target="counterweight-chatgpt" rel="noreferrer" className="self-center text-xs underline">
            {t("providers.subscriptionOpenBrowser")}
          </a>
        )}
      </div>
      {error && (
        <p role="alert" className="text-destructive text-xs">
          {error}
        </p>
      )}
      <a
        className="text-muted-foreground text-xs underline"
        href="https://chatgpt.com/#settings/Usage"
        target="_blank"
        rel="noreferrer"
      >
        {t("providers.subscriptionUsage")}
      </a>
    </div>
  );
}
