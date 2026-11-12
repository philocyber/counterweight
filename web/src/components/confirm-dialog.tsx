"use client";

import * as React from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { useI18n } from "@/i18n";

export interface ConfirmOptions {
  title: React.ReactNode;
  description?: React.ReactNode;
  /** Label of the confirm button; defaults to "Confirm". */
  confirmLabel?: React.ReactNode;
  /** Styles the confirm button as destructive. Cancel stays the focused default either way. */
  destructive?: boolean;
}

interface Pending {
  options: ConfirmOptions;
  open: boolean;
  resolve: (ok: boolean) => void;
}

/**
 * Promise-based confirmation built on AlertDialog. Render the returned element once in the
 * component and `await confirm({...})`; it resolves true only when the user presses the confirm
 * button. Escape, a click outside and Cancel all resolve false. Radix focuses Cancel on open.
 */
export function useConfirm(): [(options: ConfirmOptions) => Promise<boolean>, React.ReactElement] {
  const { t: swt } = useI18n();
  const [pending, setPending] = React.useState<Pending | null>(null);

  const confirm = React.useCallback(
    (options: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        setPending((prev) => {
          prev?.resolve(false);
          return { options, open: true, resolve };
        });
      }),
    [],
  );

  const settle = (ok: boolean) => {
    if (!pending) return;
    pending.resolve(ok);
    // Keep the content mounted while the close animation plays.
    setPending({ ...pending, open: false });
  };

  const options = pending?.options;
  const element = (
    <AlertDialog open={pending?.open ?? false} onOpenChange={(next) => !next && settle(false)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{options?.title}</AlertDialogTitle>
          {options?.description && (
            <AlertDialogDescription className="whitespace-pre-line [overflow-wrap:anywhere]">
              {options.description}
            </AlertDialogDescription>
          )}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{swt("confirm.cancel")}</AlertDialogCancel>
          <AlertDialogAction variant={options?.destructive ? "destructive" : "default"} onClick={() => settle(true)}>
            {options?.confirmLabel ?? swt("confirm.confirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );

  return [confirm, element];
}
