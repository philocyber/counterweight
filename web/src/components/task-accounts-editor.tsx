"use client";

import { PlusIcon, Trash2Icon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useI18n } from "@/i18n";

// A test account typed into the New task form. It is sent once, with the task, and stored sealed on
// the server; the form keeps it only until the task is created or the form is reset.
export interface NewTaskAccount {
  id: number;
  role: string;
  username: string;
  password: string;
}

let nextAccountId = 1;

export function emptyTaskAccount(): NewTaskAccount {
  return { id: nextAccountId++, role: "", username: "", password: "" };
}

// A row the user left completely empty is not an account.
export function filledTaskAccounts(accounts: NewTaskAccount[]): NewTaskAccount[] {
  return accounts.filter((account) => account.role.trim() || account.username.trim() || account.password);
}

// The test accounts of a new task: one row per role, with its username and password.
export function TaskAccountsEditor({
  value,
  onChange,
  agentAccess,
  onAgentAccessChange,
  showErrors,
  disabled,
}: {
  value: NewTaskAccount[];
  onChange: (next: NewTaskAccount[]) => void;
  agentAccess: boolean;
  onAgentAccessChange: (next: boolean) => void;
  // Marks the rows that have a username or a password but no role name.
  showErrors: boolean;
  disabled?: boolean;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const set = (id: number, patch: Partial<NewTaskAccount>) =>
    onChange(value.map((account) => (account.id === id ? { ...account, ...patch } : account)));

  return (
    <div className="flex min-w-0 flex-col gap-3">
      {value.length > 0 && (
        <ul className="flex min-w-0 flex-col gap-2">
          {value.map((account, index) => {
            const roleMissing =
              showErrors && account.role.trim() === "" && (account.username.trim() !== "" || account.password !== "");
            return (
              <li
                key={account.id}
                className="grid min-w-0 gap-2 rounded-lg border bg-card p-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]"
              >
                <Input
                  value={account.role}
                  maxLength={80}
                  disabled={disabled}
                  autoComplete="off"
                  placeholder={swt("taskform.accountRole")}
                  aria-label={swt("taskform.accountRoleAria", { p0: index + 1 })}
                  aria-invalid={roleMissing}
                  onChange={(event) => set(account.id, { role: event.target.value })}
                />
                <Input
                  value={account.username}
                  disabled={disabled}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder={swt("taskform.accountUsername")}
                  aria-label={swt("taskform.accountUsernameAria", { p0: index + 1 })}
                  onChange={(event) => set(account.id, { username: event.target.value })}
                />
                <Input
                  type="password"
                  value={account.password}
                  disabled={disabled}
                  autoComplete="new-password"
                  placeholder={swt("taskform.accountPassword")}
                  aria-label={swt("taskform.accountPasswordAria", { p0: index + 1 })}
                  onChange={(event) => set(account.id, { password: event.target.value })}
                />
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  className="justify-self-end"
                  disabled={disabled}
                  aria-label={swt("taskform.accountRemoveAria", { p0: index + 1 })}
                  title={swt("taskform.accountRemove")}
                  onClick={() => onChange(value.filter((item) => item.id !== account.id))}
                >
                  <Trash2Icon />
                </Button>
                {roleMissing && (
                  <p role="alert" className="text-destructive text-xs sm:col-span-4">
                    {swt("taskform.accountRoleMissing")}
                  </p>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={disabled || value.length >= 20}
          onClick={() => onChange([...value, emptyTaskAccount()])}
        >
          <PlusIcon data-icon="inline-start" />
          {swt("taskform.accountAdd")}
        </Button>
      </div>
      {value.length > 0 && (
        <div className="flex min-w-0 items-start gap-3">
          <Switch
            id="task-accounts-agent-access"
            checked={agentAccess}
            disabled={disabled}
            onCheckedChange={onAgentAccessChange}
          />
          <div className="flex min-w-0 flex-col gap-0.5">
            <label htmlFor="task-accounts-agent-access" className="font-medium text-sm">
              {swt("taskform.accountsAgentAccess")}
            </label>
            <p className="text-muted-foreground text-xs">{swt("taskform.accountsAgentAccessHint")}</p>
          </div>
        </div>
      )}
    </div>
  );
}
