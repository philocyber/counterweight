"use client";

import { useEffect, useState } from "react";

import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";
import { ManagementTransportError } from "@/lib/management-transport";

export default function SetupPage() {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const router = useRouter();
  const [password, setPassword] = useState("");
  const [setupToken, setSetupToken] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | ManagementTransportError>("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    api
      .authStatus()
      .then(({ initialized }) => {
        if (initialized) router.replace("/login");
      })
      .catch((err) => setError(err instanceof ManagementTransportError ? err : swt("interface.m0001")))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (password !== confirm) {
      setError(swt("interface.m0041"));
      return;
    }
    if (password.length < 8) {
      setError(swt("interface.m0042"));
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.initPassword(password, setupToken);
      auth.setToken(token);
      router.replace("/dashboard");
    } catch (err) {
      setError(
        err instanceof ManagementTransportError ? err : err instanceof Error ? err.message : swt("interface.m0043"),
      );
    } finally {
      setLoading(false);
    }
  }

  if (checking) return null;

  return (
    <div className="console-auth relative flex min-h-dvh">
      <div className="console-auth-brand">
        <div className="flex items-center gap-3">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/counterweight.svg" alt="" width={36} height={36} />
          <span className="font-semibold text-lg text-topbar-foreground tracking-tight">counterweight</span>
        </div>
        <p className="hidden max-w-xs text-sm text-topbar-muted md:block">{swt("app.metaDescription")}</p>
      </div>

      {/* Right panel */}
      <div className="console-auth-form flex w-full items-center justify-center p-6">
        <div className="w-full max-w-sm space-y-7 py-10">
          <div className="space-y-2">
            <h2 className="text-2xl font-semibold tracking-tight">{swt("interface.m0044")}</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">{swt("interface.m0045")}</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="setup-token">{swt("interface.setupTokenLabel")}</Label>
              <Input
                id="setup-token"
                type="password"
                value={setupToken}
                onChange={(e) => setSetupToken(e.target.value.trim())}
                autoComplete="off"
                maxLength={128}
                required
              />
              <p className="text-sm text-muted-foreground">{swt("interface.setupTokenHelp")}</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">{swt("interface.m0046")}</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={swt("interface.m0047")}
                autoFocus
                autoComplete="new-password"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm">{swt("interface.m0048")}</Label>
              <Input
                id="confirm"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder={swt("interface.m0049")}
                autoComplete="new-password"
              />
            </div>
            {error && (
              <p className="text-sm text-destructive">
                {error instanceof ManagementTransportError ? swt("common.managementHTTPSRequired") : error}
              </p>
            )}
            <Button type="submit" className="h-10 w-full" disabled={loading || !setupToken || !password || !confirm}>
              {loading ? swt("interface.m0050") : swt("interface.m0051")}
            </Button>
          </form>
        </div>
      </div>
    </div>
  );
}
