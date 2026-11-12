"use client";

import { useState } from "react";

import { Check, KeyRound, LogOut, ShieldOff } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useI18n } from "@/i18n";
import { translate as swt } from "@/i18n/runtime";
import { auth } from "@/lib/auth";
import { cn, getInitials } from "@/lib/utils";

import { ChangePasswordDialog } from "./change-password-dialog";

export function AccountSwitcher({
  users,
}: {
  readonly users: ReadonlyArray<{
    readonly id: string;
    readonly name: string;
    readonly email: string;
    readonly avatar: string;
    readonly role: string;
  }>;
}) {
  "use no memo";
  const { t: swt, locale: swLocale } = useI18n();

  const [activeUser, setActiveUser] = useState(users[0]);
  const [pwOpen, setPwOpen] = useState(false);

  async function handleLogout(everywhere = false) {
    await auth.signOut(everywhere);
    window.location.href = "/login";
  }

  if (!activeUser) {
    return null;
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label={swt("english.e214")}
            className="rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <Avatar className="size-8 rounded-lg">
              <AvatarImage src={activeUser.avatar || undefined} alt={activeUser.name} />
              <AvatarFallback>{getInitials(activeUser.name)}</AvatarFallback>
            </Avatar>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent className="min-w-56 space-y-1 rounded-lg" side="bottom" align="end" sideOffset={4}>
          {users.map((user) => (
            <DropdownMenuItem
              key={user.email}
              className={cn("p-0", user.id === activeUser.id && "bg-accent/50")}
              aria-current={user.id === activeUser.id ? "true" : undefined}
              onClick={() => setActiveUser(user)}
            >
              <div className="flex w-full items-center gap-2 px-1 py-1.5">
                <Avatar className="size-9 rounded-lg">
                  <AvatarImage src={user.avatar || undefined} alt={user.name} />
                  <AvatarFallback>{getInitials(user.name)}</AvatarFallback>
                </Avatar>
                <div className="grid min-w-0 flex-1 text-left text-sm leading-tight">
                  <span className="truncate font-semibold">{user.name}</span>
                  <span className="truncate text-xs capitalize">
                    {user.role === "operator" ? swt("app.operator") : user.role}
                  </span>
                </div>
                <span
                  className={cn(
                    "mr-1 flex size-5 items-center justify-center rounded-full text-primary opacity-0",
                    user.id === activeUser.id && "opacity-100",
                  )}
                >
                  <Check aria-hidden="true" />
                </span>
              </div>
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => setPwOpen(true)}>
            <KeyRound />
            {swt("interface.m0053")}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => void handleLogout()} className="text-destructive focus:text-destructive">
            <LogOut />
            {swt("interface.m0054")}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => void handleLogout(true)} className="text-destructive focus:text-destructive">
            <ShieldOff />
            {swt("common.signOutEverywhere")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ChangePasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
    </>
  );
}
