"use client";

import * as React from "react";

import { CopyIcon, InfoIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { useConfirm } from "@/components/confirm-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Field, FieldContent, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import { ACCOUNT_REFERENCE_FIELDS, accountReference } from "@/lib/authz";
import type { AuthzAccount } from "@/lib/types";
import { copyText } from "@/lib/utils";

// Adds a test account, or edits one. A stored password or token is never shown or prefilled: an
// empty field keeps the stored value and the checkbox removes it.
function AccountDialog({
  taskId,
  open,
  account,
  onClose,
  onSaved,
}: {
  taskId: string;
  open: boolean;
  // The account to edit, or null to add one.
  account: AuthzAccount | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [role, setRole] = React.useState("");
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [token, setToken] = React.useState("");
  const [removePassword, setRemovePassword] = React.useState(false);
  const [removeToken, setRemoveToken] = React.useState(false);
  const [agentAccess, setAgentAccess] = React.useState(false);
  const [touched, setTouched] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  // Runs when the dialog opens and when it closes, so a typed secret never outlives the dialog.
  React.useEffect(() => {
    setPassword("");
    setToken("");
    setRemovePassword(false);
    setRemoveToken(false);
    setTouched(false);
    setError("");
    if (open) {
      setRole(account?.role ?? "");
      setUsername(account?.username ?? "");
      setAgentAccess(account?.agent_access ?? false);
    }
  }, [open, account]);

  const roleMissing = role.trim() === "";

  async function submit() {
    setTouched(true);
    if (busy || roleMissing) return;
    const name = role.trim();
    const user = username.trim();
    setBusy(true);
    setError("");
    try {
      if (account) {
        const body: {
          role?: string;
          username?: string;
          password?: string;
          token?: string;
          agent_access?: boolean;
        } = {};
        if (name !== account.role) body.role = name;
        if (user !== account.username) body.username = user;
        if (removePassword) body.password = "";
        else if (password) body.password = password;
        if (removeToken) body.token = "";
        else if (token) body.token = token;
        if (agentAccess !== account.agent_access) body.agent_access = agentAccess;
        if (Object.keys(body).length > 0) await api.updateAuthzAccount(taskId, account.id, body);
        toast.success(swt("authz.accountSaved"));
      } else {
        const created = await api.createAuthzAccount(taskId, {
          role: name,
          ...(user ? { username: user } : {}),
          ...(password ? { password } : {}),
          ...(token ? { token } : {}),
          ...(agentAccess ? { agent_access: true } : {}),
        });
        toast.success(swt("authz.accountAdded", { p0: created.key }));
      }
      onSaved();
    } catch (e) {
      setError(swt("authz.accountSaveFailed", { p0: (e as Error).message }));
    } finally {
      setBusy(false);
    }
  }

  let submitLabel = account ? swt("authz.saveAccount") : swt("authz.addAccount");
  if (busy) submitLabel = account ? swt("authz.savingAccount") : swt("authz.addingAccount");

  return (
    <Dialog open={open} onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <form
          className="grid gap-4"
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{account ? swt("authz.editDialogTitle") : swt("authz.addDialogTitle")}</DialogTitle>
            <DialogDescription>
              {account ? swt("authz.editDialogDescription") : swt("authz.addDialogDescription")}
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={touched && roleMissing}>
            <FieldLabel htmlFor="authz-account-role">{swt("authz.roleLabel")}</FieldLabel>
            <Input
              id="authz-account-role"
              value={role}
              maxLength={100}
              disabled={busy}
              autoComplete="off"
              placeholder={swt("authz.rolePlaceholder")}
              aria-invalid={touched && roleMissing}
              onChange={(event) => setRole(event.target.value)}
            />
            <FieldDescription>
              {account ? swt("authz.roleHintEdit", { p0: account.key }) : swt("authz.roleHintCreate")}
            </FieldDescription>
            {touched && roleMissing && <FieldError>{swt("authz.roleRequired")}</FieldError>}
          </Field>
          <Field>
            <FieldLabel htmlFor="authz-account-username">{swt("authz.usernameLabel")}</FieldLabel>
            <Input
              id="authz-account-username"
              value={username}
              disabled={busy}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => setUsername(event.target.value)}
            />
            <FieldDescription>{swt("authz.usernameHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="authz-account-password">{swt("authz.passwordLabel")}</FieldLabel>
            <Input
              id="authz-account-password"
              type="password"
              value={password}
              disabled={busy || removePassword}
              autoComplete="new-password"
              onChange={(event) => setPassword(event.target.value)}
            />
            {account && (
              <FieldDescription>
                {account.has_password ? swt("authz.passwordStoredHint") : swt("authz.passwordNotStoredHint")}
              </FieldDescription>
            )}
            {account?.has_password && (
              <label htmlFor="authz-account-remove-password" className="flex w-fit items-center gap-2 text-sm">
                <Checkbox
                  id="authz-account-remove-password"
                  checked={removePassword}
                  disabled={busy}
                  onCheckedChange={(checked) => {
                    setRemovePassword(checked === true);
                    if (checked === true) setPassword("");
                  }}
                />
                {swt("authz.removePassword")}
              </label>
            )}
          </Field>
          <Field>
            <FieldLabel htmlFor="authz-account-token">{swt("authz.tokenLabel")}</FieldLabel>
            <Input
              id="authz-account-token"
              type="password"
              value={token}
              disabled={busy || removeToken}
              autoComplete="new-password"
              onChange={(event) => setToken(event.target.value)}
            />
            <FieldDescription>
              {swt("authz.tokenHint")}
              {account ? ` ${account.has_token ? swt("authz.tokenStoredHint") : swt("authz.tokenNotStoredHint")}` : ""}
            </FieldDescription>
            {account?.has_token && (
              <label htmlFor="authz-account-remove-token" className="flex w-fit items-center gap-2 text-sm">
                <Checkbox
                  id="authz-account-remove-token"
                  checked={removeToken}
                  disabled={busy}
                  onCheckedChange={(checked) => {
                    setRemoveToken(checked === true);
                    if (checked === true) setToken("");
                  }}
                />
                {swt("authz.removeToken")}
              </label>
            )}
          </Field>
          <Field orientation="horizontal">
            <Switch
              id="authz-account-agent-access"
              checked={agentAccess}
              disabled={busy}
              onCheckedChange={setAgentAccess}
            />
            <FieldContent>
              <FieldLabel htmlFor="authz-account-agent-access">{swt("authz.agentAccessLabel")}</FieldLabel>
              <FieldDescription>{swt("authz.agentAccessHint")}</FieldDescription>
            </FieldContent>
          </Field>
          {error && (
            <p role="alert" className="break-words text-destructive text-sm">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
              {swt("authz.cancel")}
            </Button>
            <Button type="submit" disabled={busy || (touched && roleMissing)}>
              {busy && <Spinner data-icon="inline-start" />}
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// The three texts a policy can use to take a value from an account, each copied with one click.
function ReferenceMenu({ account }: { account: AuthzAccount }) {
  "use no memo";
  const { t: swt } = useI18n();
  async function copy(reference: string) {
    if (await copyText(reference)) toast.success(swt("authz.referenceCopied"));
    else toast.error(swt("authz.copyFailed"));
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          size="xs"
          variant="ghost"
          aria-label={swt("authz.copyReferenceAria", { p0: account.role })}
        >
          <CopyIcon data-icon="inline-start" />
          {swt("authz.copyReference")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuLabel>{swt("authz.referenceMenuLabel")}</DropdownMenuLabel>
        {ACCOUNT_REFERENCE_FIELDS.map((field) => {
          const reference = accountReference(account.key, field);
          return (
            <DropdownMenuItem key={field} onSelect={() => void copy(reference)}>
              <code className="font-mono text-xs">{reference}</code>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// The test accounts of the check: the table, and the dialogs that add, edit and delete one.
export function AccountsCard({
  taskId,
  accounts,
  onChanged,
}: {
  taskId: string;
  accounts: AuthzAccount[];
  onChanged: () => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [confirm, confirmDialog] = useConfirm();
  // The account stays set while the dialog closes, so its content does not change during the animation.
  const [dialog, setDialog] = React.useState<{ open: boolean; account: AuthzAccount | null }>({
    open: false,
    account: null,
  });
  const [busyId, setBusyId] = React.useState<number | null>(null);

  async function remove(account: AuthzAccount) {
    const ok = await confirm({
      title: swt("authz.deleteTitle", { p0: account.role }),
      description: swt("authz.deleteDescription", { p0: account.key }),
      confirmLabel: swt("authz.deleteAccount"),
      destructive: true,
    });
    if (!ok) return;
    setBusyId(account.id);
    try {
      await api.deleteAuthzAccount(taskId, account.id);
      toast.success(swt("authz.accountDeleted"));
      onChanged();
    } catch (e) {
      toast.error(swt("authz.deleteFailed", { p0: (e as Error).message }));
    } finally {
      setBusyId(null);
    }
  }

  const stored = (set: boolean) => (
    <Badge variant={set ? "secondary" : "outline"}>{set ? swt("authz.secretSet") : swt("authz.secretNotSet")}</Badge>
  );

  return (
    <Card className="min-w-0">
      {confirmDialog}
      <CardHeader>
        <CardTitle className="text-base">{swt("authz.accountsTitle")}</CardTitle>
        <CardDescription>{swt("authz.accountsDescription")}</CardDescription>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-3">
        <div>
          <Button type="button" size="sm" onClick={() => setDialog({ open: true, account: null })}>
            <PlusIcon data-icon="inline-start" />
            {swt("authz.addAccount")}
          </Button>
        </div>
        {accounts.length === 0 ? (
          <p className="text-muted-foreground text-sm">{swt("authz.accountsEmpty")}</p>
        ) : (
          <div className="min-w-0 overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{swt("authz.colRole")}</TableHead>
                  <TableHead>{swt("authz.colKey")}</TableHead>
                  <TableHead>{swt("authz.colUsername")}</TableHead>
                  <TableHead>{swt("authz.colPassword")}</TableHead>
                  <TableHead>{swt("authz.colToken")}</TableHead>
                  <TableHead>{swt("authz.colAgents")}</TableHead>
                  <TableHead>
                    <span className="sr-only">{swt("authz.colActions")}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {accounts.map((account) => (
                  <TableRow key={account.id}>
                    <TableCell className="max-w-64 truncate font-medium" title={account.role}>
                      {account.role}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{account.key}</code>
                        <ReferenceMenu account={account} />
                      </div>
                    </TableCell>
                    <TableCell className="max-w-64 truncate" title={account.username}>
                      {account.username || <span className="text-muted-foreground">{swt("authz.secretNotSet")}</span>}
                    </TableCell>
                    <TableCell>{stored(account.has_password)}</TableCell>
                    <TableCell>{stored(account.has_token)}</TableCell>
                    <TableCell>
                      <Badge variant={account.agent_access ? "secondary" : "outline"}>
                        {account.agent_access ? swt("authz.agentsAllowed") : swt("authz.agentsNotAllowed")}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1">
                        <Button
                          type="button"
                          size="icon-sm"
                          variant="ghost"
                          disabled={busyId === account.id}
                          aria-label={swt("authz.editAccountAria", { p0: account.role })}
                          title={swt("authz.editAccount")}
                          onClick={() => setDialog({ open: true, account })}
                        >
                          <PencilIcon />
                        </Button>
                        <Button
                          type="button"
                          size="icon-sm"
                          variant="ghost"
                          disabled={busyId === account.id}
                          aria-label={swt("authz.deleteAccountAria", { p0: account.role })}
                          title={swt("authz.deleteAccount")}
                          onClick={() => void remove(account)}
                        >
                          {busyId === account.id ? <Spinner /> : <Trash2Icon />}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <Alert>
          <InfoIcon />
          <AlertDescription>{swt("authz.secretsNote")}</AlertDescription>
        </Alert>
      </CardContent>
      <AccountDialog
        taskId={taskId}
        open={dialog.open}
        account={dialog.account}
        onClose={() => setDialog((current) => ({ ...current, open: false }))}
        onSaved={() => {
          setDialog((current) => ({ ...current, open: false }));
          onChanged();
        }}
      />
    </Card>
  );
}
