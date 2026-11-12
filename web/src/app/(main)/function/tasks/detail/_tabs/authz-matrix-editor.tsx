"use client";

import * as React from "react";

import {
  ChevronRightIcon,
  EllipsisIcon,
  PlusIcon,
  SlidersHorizontalIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react";

import { useConfirm } from "@/components/confirm-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useI18n } from "@/i18n";
import { accountReference } from "@/lib/authz";
import {
  caseIdFor,
  changesData,
  DEFAULT_LOGIN,
  expectationOf,
  fitsLines,
  hasGeneratedId,
  identityOf,
  linesOf,
  POLICY_METHODS,
  parseStatuses,
  personaForAccount,
  type SignIn,
  signInOf,
  verdictOf,
  withIdentity,
  withSignIn,
  withVerdict,
} from "@/lib/authz-policy";
import type { AuthzAccount, AuthzDocCase, AuthzDocExpectation, AuthzDocPersona, AuthzDocument } from "@/lib/types";
import { cn } from "@/lib/utils";

// The policy as a matrix: requests down, roles across, and in every cell whether the role may do
// the request. It edits a document; the tab writes that document back as policy text and the
// server checks it like any other policy.

// The status a cell expects. The typed text is kept while it is not a list of status codes, so a
// half-typed value is not thrown away.
function StatusInput({
  codes,
  label,
  disabled,
  onChange,
}: {
  codes: number[];
  label: string;
  disabled?: boolean;
  onChange: (codes: number[]) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const shown = codes.join(", ");
  const [typed, setTyped] = React.useState<string | null>(null);
  const value = typed ?? shown;
  return (
    <Input
      value={value}
      disabled={disabled}
      inputMode="numeric"
      autoComplete="off"
      aria-label={label}
      title={swt("authz.mxStatusTitle")}
      aria-invalid={parseStatuses(value) === null}
      className="h-7 w-[4.5rem] px-1.5 font-mono text-xs md:text-xs"
      onChange={(event) => {
        const next = event.target.value;
        const parsed = parseStatuses(next);
        setTyped(next);
        if (parsed) onChange(parsed);
      }}
      onBlur={() => setTyped(null)}
    />
  );
}

// One cell: may this role do this request, and which status says so.
function VerdictCell({
  expectation,
  denyStatus,
  role,
  request,
  disabled,
  onChange,
}: {
  expectation: AuthzDocExpectation | null | undefined;
  // The status a new denial starts with: 401 for a visitor, 403 for a signed-in role.
  denyStatus: number;
  role: string;
  request: string;
  disabled?: boolean;
  onChange: (next: AuthzDocExpectation) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const verdict = verdictOf(expectation);
  const codes = (verdict === "allow" ? expectation?.allow : expectation?.deny) ?? [];
  const marked =
    (expectation?.body_contains?.length ?? 0) > 0 ||
    (expectation?.body_not_contains?.length ?? 0) > 0 ||
    !!expectation?.location_contains;
  const choose = (next: "allow" | "deny") => {
    if (next === verdict) return;
    onChange(withVerdict(expectation, next, next === "allow" ? [200] : [denyStatus]));
  };
  return (
    <div className="flex items-center gap-1.5">
      {/* biome-ignore lint/a11y/useSemanticElements: two toggle buttons, not a form field set */}
      <div
        role="group"
        aria-label={swt("authz.mxVerdictAria", { p0: role, p1: request })}
        className={cn(
          "inline-flex shrink-0 overflow-hidden rounded-md border",
          verdict === null && "border-amber-500 border-dashed",
        )}
      >
        <button
          type="button"
          disabled={disabled}
          aria-pressed={verdict === "allow"}
          className={cn(
            "px-2 py-1 font-medium text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
            verdict === "allow" ? "bg-emerald-700 text-white" : "hover:bg-muted",
          )}
          onClick={() => choose("allow")}
        >
          {swt("authz.mxAllow")}
        </button>
        <button
          type="button"
          disabled={disabled}
          aria-pressed={verdict === "deny"}
          className={cn(
            "border-l px-2 py-1 font-medium text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
            verdict === "deny" ? "bg-red-700 text-white" : "hover:bg-muted",
          )}
          onClick={() => choose("deny")}
        >
          {swt("authz.mxDeny")}
        </button>
      </div>
      {verdict && (
        <StatusInput
          codes={codes}
          disabled={disabled}
          label={swt("authz.mxStatusAria", { p0: role, p1: request })}
          onChange={(next) => onChange(withVerdict(expectation, verdict, next))}
        />
      )}
      {marked && (
        <Badge variant="outline" title={swt("authz.mxMarkedTitle")}>
          {swt("authz.mxMarked")}
        </Badge>
      )}
    </div>
  );
}

// How one role signs in, and the page that proves the session is that role's.
function SignInDialog({
  persona,
  role,
  account,
  others,
  onClose,
  onApply,
}: {
  // The persona to edit, or null while the dialog is closed.
  persona: AuthzDocPersona | null;
  role: string;
  account: AuthzAccount | undefined;
  // How many other roles could take the same sign-in.
  others: number;
  onClose: () => void;
  onApply: (signIn: SignIn | null, identityPath: string, identityTexts: string[], toOthers: boolean) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [kind, setKind] = React.useState<"login" | "token">("login");
  const [login, setLogin] = React.useState(DEFAULT_LOGIN);
  const [tokenHeader, setTokenHeader] = React.useState("Authorization");
  const [tokenScheme, setTokenScheme] = React.useState("Bearer");
  const [identityPath, setIdentityPath] = React.useState("");
  const [identityTexts, setIdentityTexts] = React.useState("");
  const [toOthers, setToOthers] = React.useState(true);
  const [checked, setChecked] = React.useState(false);

  const shape = persona ? signInOf(persona) : null;
  // Written in a way the form does not show: only the proof below can be changed here.
  const custom = persona !== null && shape === null;

  // biome-ignore lint/correctness/useExhaustiveDependencies: the form is filled when the dialog opens for a persona, not on every edit of the document
  React.useEffect(() => {
    if (!persona) return;
    const current = signInOf(persona);
    setKind(current?.kind === "token" ? "token" : "login");
    setLogin(current?.kind === "login" ? current : DEFAULT_LOGIN);
    setTokenHeader(current?.kind === "token" ? current.header : "Authorization");
    setTokenScheme(current?.kind === "token" ? current.scheme : "Bearer");
    const identity = identityOf(persona);
    setIdentityPath(identity.path || "/api/me");
    setIdentityTexts(identity.texts.join("\n"));
    setToOthers(true);
    setChecked(false);
  }, [persona?.key]);

  // The proof texts as the policy has them: handed back unchanged unless the field is edited.
  const savedTexts = persona ? identityOf(persona).texts : [];
  const proof = linesOf(identityTexts, savedTexts);
  const missing = (value: string) => checked && value.trim() === "";
  const badPath = (value: string) => checked && !value.trim().startsWith("/");
  const incomplete =
    !identityPath.trim().startsWith("/") ||
    proof.length === 0 ||
    (!custom &&
      (kind === "login"
        ? !login.path.trim().startsWith("/") ||
          !login.usernameField.trim() ||
          !login.passwordField.trim() ||
          (login.session === "token" && (!login.pointer.trim().startsWith("/") || !login.header.trim()))
        : !tokenHeader.trim()));

  function submit() {
    setChecked(true);
    if (incomplete) return;
    let signIn: SignIn | null = null;
    if (!custom) signIn = kind === "login" ? login : { kind: "token", header: tokenHeader, scheme: tokenScheme };
    onApply(signIn, identityPath, proof, !custom && others > 0 && toOthers);
  }

  const key = persona?.key ?? "";
  return (
    <Dialog open={persona !== null} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
        <form
          className="grid gap-4"
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault();
            event.stopPropagation();
            submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{swt("authz.siTitle", { p0: role })}</DialogTitle>
            <DialogDescription>{swt("authz.siDescription")}</DialogDescription>
          </DialogHeader>
          {!account && (
            <p role="alert" className="flex items-start gap-2 text-amber-700 text-sm dark:text-amber-400">
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
              {swt("authz.siNoAccount", { p0: key })}
            </p>
          )}
          {custom ? (
            <p className="rounded-lg border bg-muted/50 p-3 text-sm">{swt("authz.siCustom")}</p>
          ) : (
            <>
              <Field>
                <FieldLabel htmlFor="authz-si-kind">{swt("authz.siKindLabel")}</FieldLabel>
                <NativeSelect
                  id="authz-si-kind"
                  className="w-full"
                  value={kind}
                  onChange={(event) => setKind(event.target.value === "token" ? "token" : "login")}
                >
                  <NativeSelectOption value="login">{swt("authz.siKindLogin")}</NativeSelectOption>
                  <NativeSelectOption value="token">{swt("authz.siKindToken")}</NativeSelectOption>
                </NativeSelect>
              </Field>
              {kind === "login" ? (
                <>
                  <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_9rem]">
                    <Field data-invalid={badPath(login.path)}>
                      <FieldLabel htmlFor="authz-si-path">{swt("authz.siPath")}</FieldLabel>
                      <Input
                        id="authz-si-path"
                        className="font-mono text-xs md:text-xs"
                        value={login.path}
                        spellCheck={false}
                        aria-invalid={badPath(login.path)}
                        onChange={(event) => setLogin({ ...login, path: event.target.value })}
                      />
                      {badPath(login.path) && <FieldError>{swt("authz.siPathError")}</FieldError>}
                    </Field>
                    <Field>
                      <FieldLabel htmlFor="authz-si-format">{swt("authz.siFormat")}</FieldLabel>
                      <NativeSelect
                        id="authz-si-format"
                        className="w-full"
                        value={login.format}
                        onChange={(event) =>
                          setLogin({ ...login, format: event.target.value === "form" ? "form" : "json" })
                        }
                      >
                        <NativeSelectOption value="json">{swt("authz.siFormatJson")}</NativeSelectOption>
                        <NativeSelectOption value="form">{swt("authz.siFormatForm")}</NativeSelectOption>
                      </NativeSelect>
                    </Field>
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Field data-invalid={missing(login.usernameField)}>
                      <FieldLabel htmlFor="authz-si-username">{swt("authz.siUsernameField")}</FieldLabel>
                      <Input
                        id="authz-si-username"
                        className="font-mono text-xs md:text-xs"
                        value={login.usernameField}
                        spellCheck={false}
                        aria-invalid={missing(login.usernameField)}
                        onChange={(event) => setLogin({ ...login, usernameField: event.target.value })}
                      />
                    </Field>
                    <Field data-invalid={missing(login.passwordField)}>
                      <FieldLabel htmlFor="authz-si-password">{swt("authz.siPasswordField")}</FieldLabel>
                      <Input
                        id="authz-si-password"
                        className="font-mono text-xs md:text-xs"
                        value={login.passwordField}
                        spellCheck={false}
                        aria-invalid={missing(login.passwordField)}
                        onChange={(event) => setLogin({ ...login, passwordField: event.target.value })}
                      />
                    </Field>
                  </div>
                  <FieldDescription>{swt("authz.siFieldsHint")}</FieldDescription>
                  <Field>
                    <FieldLabel htmlFor="authz-si-session">{swt("authz.siSession")}</FieldLabel>
                    <NativeSelect
                      id="authz-si-session"
                      className="w-full"
                      value={login.session}
                      onChange={(event) =>
                        setLogin({ ...login, session: event.target.value === "cookie" ? "cookie" : "token" })
                      }
                    >
                      <NativeSelectOption value="token">{swt("authz.siSessionToken")}</NativeSelectOption>
                      <NativeSelectOption value="cookie">{swt("authz.siSessionCookie")}</NativeSelectOption>
                    </NativeSelect>
                  </Field>
                  {login.session === "token" && (
                    <div className="grid gap-4 sm:grid-cols-3">
                      <Field data-invalid={badPath(login.pointer)}>
                        <FieldLabel htmlFor="authz-si-pointer">{swt("authz.siPointer")}</FieldLabel>
                        <Input
                          id="authz-si-pointer"
                          className="font-mono text-xs md:text-xs"
                          value={login.pointer}
                          spellCheck={false}
                          aria-invalid={badPath(login.pointer)}
                          onChange={(event) => setLogin({ ...login, pointer: event.target.value })}
                        />
                      </Field>
                      <Field data-invalid={missing(login.header)}>
                        <FieldLabel htmlFor="authz-si-header">{swt("authz.siHeader")}</FieldLabel>
                        <Input
                          id="authz-si-header"
                          className="font-mono text-xs md:text-xs"
                          value={login.header}
                          spellCheck={false}
                          aria-invalid={missing(login.header)}
                          onChange={(event) => setLogin({ ...login, header: event.target.value })}
                        />
                      </Field>
                      <Field>
                        <FieldLabel htmlFor="authz-si-prefix">{swt("authz.siPrefix")}</FieldLabel>
                        <Input
                          id="authz-si-prefix"
                          className="font-mono text-xs md:text-xs"
                          value={login.prefix}
                          spellCheck={false}
                          onChange={(event) => setLogin({ ...login, prefix: event.target.value })}
                        />
                      </Field>
                      <FieldDescription className="sm:col-span-3">{swt("authz.siTokenHint")}</FieldDescription>
                    </div>
                  )}
                </>
              ) : (
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field data-invalid={missing(tokenHeader)}>
                    <FieldLabel htmlFor="authz-si-token-header">{swt("authz.siHeader")}</FieldLabel>
                    <Input
                      id="authz-si-token-header"
                      className="font-mono text-xs md:text-xs"
                      value={tokenHeader}
                      spellCheck={false}
                      aria-invalid={missing(tokenHeader)}
                      onChange={(event) => setTokenHeader(event.target.value)}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="authz-si-scheme">{swt("authz.siScheme")}</FieldLabel>
                    <Input
                      id="authz-si-scheme"
                      className="font-mono text-xs md:text-xs"
                      value={tokenScheme}
                      spellCheck={false}
                      onChange={(event) => setTokenScheme(event.target.value)}
                    />
                  </Field>
                  <FieldDescription className="sm:col-span-2">{swt("authz.siSchemeHint")}</FieldDescription>
                </div>
              )}
            </>
          )}
          <div className="grid gap-4 border-t pt-4">
            <div className="flex flex-col gap-1">
              <p className="font-medium text-sm">{swt("authz.siIdentityTitle")}</p>
              <p className="text-muted-foreground text-sm">{swt("authz.siIdentityHint")}</p>
            </div>
            <Field data-invalid={badPath(identityPath)}>
              <FieldLabel htmlFor="authz-si-identity-path">{swt("authz.siIdentityPath")}</FieldLabel>
              <Input
                id="authz-si-identity-path"
                className="font-mono text-xs md:text-xs"
                value={identityPath}
                spellCheck={false}
                aria-invalid={badPath(identityPath)}
                onChange={(event) => setIdentityPath(event.target.value)}
              />
              {badPath(identityPath) && <FieldError>{swt("authz.siPathError")}</FieldError>}
            </Field>
            <Field data-invalid={checked && proof.length === 0}>
              <FieldLabel htmlFor="authz-si-identity-texts">{swt("authz.siIdentityTexts")}</FieldLabel>
              <Textarea
                id="authz-si-identity-texts"
                className="min-h-16 font-mono text-xs md:text-xs"
                value={identityTexts}
                spellCheck={false}
                disabled={!fitsLines(savedTexts)}
                aria-invalid={checked && proof.length === 0}
                onChange={(event) => setIdentityTexts(event.target.value)}
              />
              <FieldDescription>
                {fitsLines(savedTexts)
                  ? swt("authz.siIdentityTextsHint", { p0: accountReference(key, "username") })
                  : swt("authz.textOnly")}
              </FieldDescription>
              {checked && proof.length === 0 && <FieldError>{swt("authz.siIdentityTextsError")}</FieldError>}
            </Field>
          </div>
          {!custom && others > 0 && (
            <label htmlFor="authz-si-others" className="flex items-start gap-2 text-sm">
              <Checkbox
                id="authz-si-others"
                className="mt-0.5"
                checked={toOthers}
                onCheckedChange={(value) => setToOthers(value === true)}
              />
              <span className="flex flex-col gap-0.5">
                <span className="font-medium">{swt("authz.siApplyAll")}</span>
                <span className="text-muted-foreground text-xs">{swt("authz.siApplyAllHint")}</span>
              </span>
            </label>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {swt("authz.cancel")}
            </Button>
            <Button type="submit">{swt("authz.mxApply")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// Largest whole number a JSON body can hold without the browser rounding it.
function exactNumbers(value: unknown): boolean {
  if (typeof value === "number") return !Number.isInteger(value) || Number.isSafeInteger(value);
  if (Array.isArray(value)) return value.every(exactNumbers);
  if (value && typeof value === "object") return Object.values(value).every(exactNumbers);
  return true;
}

// What a cell does not show: the name of the request, its body, and what the answer is checked for
// beyond its status.
function CaseDialog({
  item,
  personas,
  onClose,
  onApply,
}: {
  // The case to edit, or null while the dialog is closed.
  item: AuthzDocCase | null;
  personas: { key: string; role: string }[];
  onClose: () => void;
  onApply: (next: AuthzDocCase) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [id, setId] = React.useState("");
  const [body, setBody] = React.useState("");
  const [denyAll, setDenyAll] = React.useState(false);
  // The texts each role's answer is checked for, as shown in the fields; keyed by persona.
  const [checks, setChecks] = React.useState(new Map<string, { has: string; lacks: string; location: string }>());
  const [bodyError, setBodyError] = React.useState("");

  // biome-ignore lint/correctness/useExhaustiveDependencies: the form is filled when the dialog opens for a case
  React.useEffect(() => {
    if (!item) return;
    setId(item.id);
    const json = item.request.json;
    setBody(json === undefined || json === null ? "" : JSON.stringify(json, null, 2));
    setDenyAll(item.deny_all === true);
    setBodyError("");
    setChecks(
      new Map(
        Object.entries(item.expect ?? {}).map(([key, e]) => [
          key,
          {
            has: (e?.body_contains ?? []).join("\n"),
            lacks: (e?.body_not_contains ?? []).join("\n"),
            location: e?.location_contains ?? "",
          },
        ]),
      ),
    );
  }, [item]);

  if (!item) return <Dialog open={false} />;
  const otherBody = (item.request.form !== undefined && item.request.form !== null) || !!item.request.body;
  const judged = personas.filter((p) => verdictOf(expectationOf(item, p.key)) !== null);
  const blank = { has: "", lacks: "", location: "" };

  function submit() {
    if (!item) return;
    const request = { ...item.request };
    if (!otherBody) {
      if (body.trim() === "") delete request.json;
      else {
        let parsed: unknown;
        try {
          parsed = JSON.parse(body);
        } catch {
          setBodyError(swt("authz.cdBodyInvalid"));
          return;
        }
        if (!exactNumbers(parsed)) {
          setBodyError(swt("authz.cdBodyNumber"));
          return;
        }
        request.json = parsed;
      }
    }
    const expect: Record<string, AuthzDocExpectation | null> = { ...(item.expect ?? {}) };
    for (const p of judged) {
      const current = expectationOf(item, p.key);
      const typed = checks.get(p.key);
      if (!current || !typed) continue;
      // A field that was not touched hands back exactly what the policy had.
      const has = linesOf(typed.has, current.body_contains);
      const lacks = linesOf(typed.lacks, current.body_not_contains);
      const location = typed.location === (current.location_contains ?? "") ? typed.location : typed.location.trim();
      const { body_contains: _has, body_not_contains: _lacks, location_contains: _location, ...verdict } = current;
      expect[p.key] = {
        ...verdict,
        ...(has.length > 0 ? { body_contains: has } : {}),
        ...(lacks.length > 0 ? { body_not_contains: lacks } : {}),
        ...(location ? { location_contains: location } : {}),
      };
    }
    const next: AuthzDocCase = { ...item, id: id.trim(), request, expect };
    if (denyAll) next.deny_all = true;
    else delete next.deny_all;
    onApply(next);
  }

  const setCheck = (key: string, patch: Partial<typeof blank>) =>
    setChecks((current) => new Map(current).set(key, { ...(current.get(key) ?? blank), ...patch }));

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
        <form
          className="grid gap-4"
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault();
            event.stopPropagation();
            submit();
          }}
        >
          <DialogHeader>
            <DialogTitle>{swt("authz.cdTitle")}</DialogTitle>
            <DialogDescription>
              <code className="font-mono text-xs">
                {item.request.method} {item.request.path}
              </code>
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="authz-cd-id">{swt("authz.cdId")}</FieldLabel>
            <Input
              id="authz-cd-id"
              className="font-mono text-xs md:text-xs"
              value={id}
              maxLength={64}
              spellCheck={false}
              onChange={(event) => setId(event.target.value)}
            />
            <FieldDescription>{swt("authz.cdIdHint")}</FieldDescription>
          </Field>
          {otherBody ? (
            <p className="rounded-lg border bg-muted/50 p-3 text-sm">{swt("authz.cdBodyOther")}</p>
          ) : (
            <Field data-invalid={bodyError !== ""}>
              <FieldLabel htmlFor="authz-cd-body">{swt("authz.cdBody")}</FieldLabel>
              <Textarea
                id="authz-cd-body"
                className="min-h-20 font-mono text-xs md:text-xs"
                value={body}
                spellCheck={false}
                placeholder='{ "name": "example" }'
                aria-invalid={bodyError !== ""}
                onChange={(event) => {
                  setBody(event.target.value);
                  setBodyError("");
                }}
              />
              <FieldDescription>{swt("authz.cdBodyHint")}</FieldDescription>
              {bodyError && <FieldError>{bodyError}</FieldError>}
            </Field>
          )}
          <label htmlFor="authz-cd-deny-all" className="flex items-start gap-2 text-sm">
            <Checkbox
              id="authz-cd-deny-all"
              className="mt-0.5"
              checked={denyAll}
              onCheckedChange={(value) => setDenyAll(value === true)}
            />
            <span className="flex flex-col gap-0.5">
              <span className="font-medium">{swt("authz.cdDenyAll")}</span>
              <span className="text-muted-foreground text-xs">{swt("authz.cdDenyAllHint")}</span>
            </span>
          </label>
          {judged.length > 0 && (
            // Rarely needed, so it stays folded unless the case already uses it.
            <details
              className="group border-t pt-4"
              open={judged.some((p) => {
                const e = expectationOf(item, p.key);
                return (
                  (e?.body_contains?.length ?? 0) > 0 ||
                  (e?.body_not_contains?.length ?? 0) > 0 ||
                  !!e?.location_contains
                );
              })}
            >
              <summary className="flex cursor-pointer list-none items-center gap-2 font-medium text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                <ChevronRightIcon className="size-4 text-muted-foreground transition-transform group-open:rotate-90" />
                {swt("authz.cdMarkersTitle")}
              </summary>
              <div className="grid gap-3 pt-3">
                <p className="text-muted-foreground text-sm">{swt("authz.cdMarkersHint")}</p>
                {judged.map((p) => {
                  const saved = expectationOf(item, p.key);
                  const typed = checks.get(p.key) ?? blank;
                  const hasFits = fitsLines(saved?.body_contains);
                  const lacksFits = fitsLines(saved?.body_not_contains);
                  return (
                    <fieldset key={p.key} className="grid gap-2 rounded-lg border p-3">
                      <legend className="px-1 font-medium text-sm">
                        {p.role}
                        <span className="font-normal text-muted-foreground">
                          {" · "}
                          {verdictOf(saved) === "allow" ? swt("authz.mxAllow") : swt("authz.mxDeny")}
                        </span>
                      </legend>
                      <div className="grid gap-2 sm:grid-cols-2">
                        <Field>
                          <FieldLabel htmlFor={`authz-cd-has-${p.key}`}>{swt("authz.cdBodyContains")}</FieldLabel>
                          <Textarea
                            id={`authz-cd-has-${p.key}`}
                            className="min-h-14 font-mono text-xs md:text-xs"
                            value={typed.has}
                            spellCheck={false}
                            disabled={!hasFits}
                            onChange={(event) => setCheck(p.key, { has: event.target.value })}
                          />
                        </Field>
                        <Field>
                          <FieldLabel htmlFor={`authz-cd-lacks-${p.key}`}>{swt("authz.cdBodyNotContains")}</FieldLabel>
                          <Textarea
                            id={`authz-cd-lacks-${p.key}`}
                            className="min-h-14 font-mono text-xs md:text-xs"
                            value={typed.lacks}
                            spellCheck={false}
                            disabled={!lacksFits}
                            onChange={(event) => setCheck(p.key, { lacks: event.target.value })}
                          />
                        </Field>
                      </div>
                      {(!hasFits || !lacksFits) && (
                        <p className="text-muted-foreground text-xs">{swt("authz.textOnly")}</p>
                      )}
                      <Field>
                        <FieldLabel htmlFor={`authz-cd-location-${p.key}`}>{swt("authz.cdLocation")}</FieldLabel>
                        <Input
                          id={`authz-cd-location-${p.key}`}
                          className="font-mono text-xs md:text-xs"
                          value={typed.location}
                          spellCheck={false}
                          placeholder="/login"
                          onChange={(event) => setCheck(p.key, { location: event.target.value })}
                        />
                      </Field>
                    </fieldset>
                  );
                })}
                <p className="text-muted-foreground text-xs">{swt("authz.cdMarkersLines")}</p>
              </div>
            </details>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {swt("authz.cancel")}
            </Button>
            <Button type="submit">{swt("authz.mxApply")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function MatrixEditor({
  doc,
  accounts,
  disabled,
  onChange,
}: {
  doc: AuthzDocument;
  accounts: AuthzAccount[];
  disabled?: boolean;
  onChange: (next: AuthzDocument) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [confirm, confirmDialog] = useConfirm();
  const [signInKey, setSignInKey] = React.useState<string | null>(null);
  const [detailsIndex, setDetailsIndex] = React.useState<number | null>(null);

  const accountByKey = new Map(accounts.map((account) => [account.key, account]));
  const roleOf = (p: AuthzDocPersona) => accountByKey.get(p.key)?.role ?? p.role ?? p.key;
  const shapes = new Map(doc.personas.map((p) => [p.key, signInOf(p)]));
  const isAnonymous = (key: string) => shapes.get(key)?.kind === "anonymous";
  // Roles that sign in with a stored account in a way the form shows.
  const formRoles = doc.personas.filter((p) => {
    const shape = shapes.get(p.key);
    return shape && shape.kind !== "anonymous" && accountByKey.has(p.key);
  });

  const setCase = (index: number, next: AuthzDocCase) =>
    onChange({ ...doc, cases: doc.cases.map((c, i) => (i === index ? next : c)) });

  function setRequest(index: number, patch: { method?: string; path?: string }) {
    const current = doc.cases[index];
    const request = { ...current.request, ...patch };
    const next: AuthzDocCase = { ...current, request };
    // A case that was never named keeps the name of its request.
    if (hasGeneratedId(current)) {
      const taken = doc.cases.filter((_, i) => i !== index).map((c) => c.id);
      next.id = caseIdFor(request.method ?? "", request.path ?? "", taken);
    }
    if (!changesData(request.method ?? "")) delete next.allow_write;
    setCase(index, next);
  }

  function setExpectation(index: number, key: string, next: AuthzDocExpectation) {
    const current = doc.cases[index];
    setCase(index, { ...current, expect: { ...(current.expect ?? {}), [key]: next } });
  }

  function addCase() {
    const request = { method: "GET", path: "/" };
    const id = caseIdFor(
      request.method,
      request.path,
      doc.cases.map((c) => c.id),
    );
    onChange({ ...doc, cases: [...doc.cases, { id, request, expect: {} }] });
  }

  function addAccount(account: AuthzAccount) {
    const persona = personaForAccount(account, formRoles[0]);
    onChange({ ...doc, personas: [...doc.personas, persona] });
    setSignInKey(persona.key);
  }

  async function removePersona(p: AuthzDocPersona) {
    const ok = await confirm({
      title: swt("authz.mxRemoveRoleTitle", { p0: roleOf(p) }),
      description: swt("authz.mxRemoveRoleBody"),
      confirmLabel: swt("authz.mxRemoveRole"),
      destructive: true,
    });
    if (!ok) return;
    onChange({
      ...doc,
      personas: doc.personas.filter((item) => item.key !== p.key),
      cases: doc.cases.map((c) => {
        if (!c.expect || !Object.hasOwn(c.expect, p.key)) return c;
        const { [p.key]: _removed, ...expect } = c.expect;
        return { ...c, expect };
      }),
    });
  }

  function applySignIn(key: string, signIn: SignIn | null, path: string, texts: string[], toOthers: boolean) {
    onChange({
      ...doc,
      personas: doc.personas.map((p) => {
        if (p.key === key) return withIdentity(signIn ? withSignIn(p, signIn) : p, path, texts);
        if (!toOthers || !signIn || !formRoles.includes(p)) return p;
        // Each of the others keeps its own proof; one that has none looks for its own username.
        let own = identityOf(p).texts;
        const account = accountByKey.get(p.key);
        if (own.length === 0 && account?.username) own = [accountReference(p.key, "username")];
        return withIdentity(withSignIn(p, signIn), path, own);
      }),
    });
    setSignInKey(null);
  }

  const signInPersona = doc.personas.find((p) => p.key === signInKey) ?? null;
  const unused = accounts.filter((account) => !doc.personas.some((p) => p.key === account.key));
  const hasAnonymous = doc.personas.some((p) => p.key === "anonymous");
  const plainHttp = doc.target.base_url.trim().toLowerCase().startsWith("http://");

  function summary(p: AuthzDocPersona): string {
    const shape = shapes.get(p.key);
    if (!shape) return swt("authz.mxSignInCustom");
    if (shape.kind === "anonymous") return swt("authz.mxSignInAnonymous");
    if (shape.kind === "token") return swt("authz.mxSignInToken");
    return swt("authz.mxSignInLogin", { p0: shape.path });
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {confirmDialog}
      <Field>
        <FieldLabel htmlFor="authz-mx-base-url">{swt("authz.mxTargetLabel")}</FieldLabel>
        <Input
          id="authz-mx-base-url"
          className="max-w-xl font-mono text-xs md:text-xs"
          value={doc.target.base_url}
          disabled={disabled}
          spellCheck={false}
          autoComplete="off"
          placeholder="https://staging.example.com"
          onChange={(event) => {
            const base_url = event.target.value;
            const target = { ...doc.target, base_url };
            // The confirmation belongs to an http:// address; it does not carry over to another one.
            if (!base_url.trim().toLowerCase().startsWith("http://")) delete target.allow_plain_http;
            onChange({ ...doc, target });
          }}
        />
        <FieldDescription>{swt("authz.mxTargetHint")}</FieldDescription>
        {plainHttp && (
          <label htmlFor="authz-mx-plain-http" className="flex w-fit items-start gap-2 text-sm">
            <Checkbox
              id="authz-mx-plain-http"
              className="mt-0.5"
              checked={doc.target.allow_plain_http === true}
              disabled={disabled}
              onCheckedChange={(value) => {
                const target = { ...doc.target };
                if (value === true) target.allow_plain_http = true;
                else delete target.allow_plain_http;
                onChange({ ...doc, target });
              }}
            />
            {swt("authz.mxPlainHttp")}
          </label>
        )}
      </Field>

      <div className="flex min-w-0 flex-col gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button type="button" size="sm" variant="outline" disabled={disabled}>
                <PlusIcon data-icon="inline-start" />
                {swt("authz.mxAddRole")}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="max-w-80">
              {unused.length > 0 && <DropdownMenuLabel>{swt("authz.mxAddRoleAccounts")}</DropdownMenuLabel>}
              {unused.map((account) => (
                <DropdownMenuItem key={account.id} onSelect={() => addAccount(account)}>
                  <span className="truncate">{account.role}</span>
                </DropdownMenuItem>
              ))}
              {!hasAnonymous && (
                <DropdownMenuItem
                  onSelect={() => onChange({ ...doc, personas: [{ key: "anonymous" }, ...doc.personas] })}
                >
                  {swt("authz.mxAddAnonymous")}
                </DropdownMenuItem>
              )}
              {unused.length === 0 && hasAnonymous && (
                <p className="px-2 py-1.5 text-muted-foreground text-xs">{swt("authz.mxAddRoleNone")}</p>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button type="button" size="sm" variant="outline" disabled={disabled} onClick={addCase}>
            <PlusIcon data-icon="inline-start" />
            {swt("authz.mxAddRequest")}
          </Button>
        </div>

        {doc.personas.length === 0 && <p className="text-muted-foreground text-sm">{swt("authz.mxNoRoles")}</p>}
        {doc.cases.length === 0 && <p className="text-muted-foreground text-sm">{swt("authz.mxNoRequests")}</p>}

        {(doc.personas.length > 0 || doc.cases.length > 0) && (
          <div className="min-w-0 overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="min-w-80 align-bottom">{swt("authz.mxRequest")}</TableHead>
                  {doc.personas.map((p) => (
                    <TableHead key={p.key} className="min-w-44 py-2 align-bottom">
                      <div className="flex min-w-0 items-center gap-1">
                        <span className="truncate font-medium text-foreground" title={roleOf(p)}>
                          {roleOf(p)}
                        </span>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              type="button"
                              size="icon-sm"
                              variant="ghost"
                              className="size-6"
                              disabled={disabled}
                              aria-label={swt("authz.mxRoleMenuAria", { p0: roleOf(p) })}
                            >
                              <EllipsisIcon />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="start">
                            {!isAnonymous(p.key) && (
                              <DropdownMenuItem onSelect={() => setSignInKey(p.key)}>
                                {swt("authz.mxEditSignIn")}
                              </DropdownMenuItem>
                            )}
                            <DropdownMenuItem variant="destructive" onSelect={() => void removePersona(p)}>
                              {swt("authz.mxRemoveRole")}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                      <div className="flex min-w-0 items-center gap-1 font-normal text-muted-foreground text-xs">
                        {!isAnonymous(p.key) && !accountByKey.has(p.key) ? (
                          <span className="flex items-center gap-1 text-amber-700 dark:text-amber-400">
                            <TriangleAlertIcon className="size-3 shrink-0" aria-hidden="true" />
                            {swt("authz.mxNoAccount")}
                          </span>
                        ) : (
                          <span className="truncate" title={summary(p)}>
                            {summary(p)}
                          </span>
                        )}
                      </div>
                    </TableHead>
                  ))}
                  <TableHead>
                    <span className="sr-only">{swt("authz.colActions")}</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {doc.cases.map((c, index) => {
                  const method = c.request.method ?? "";
                  const label = `${method} ${c.request.path ?? ""}`.trim() || String(index + 1);
                  const write = changesData(method);
                  return (
                    // biome-ignore lint/suspicious/noArrayIndexKey: rows are edited in place and an id may be empty or repeated while it is typed
                    <TableRow key={index}>
                      <TableCell className="align-top">
                        <div className="flex min-w-0 flex-col gap-1.5">
                          <div className="flex min-w-0 items-center gap-1.5">
                            <NativeSelect
                              size="sm"
                              className="shrink-0"
                              value={method}
                              disabled={disabled}
                              aria-label={swt("authz.mxMethodAria", { p0: index + 1 })}
                              onChange={(event) => setRequest(index, { method: event.target.value })}
                            >
                              {!POLICY_METHODS.includes(method as (typeof POLICY_METHODS)[number]) && (
                                <NativeSelectOption value={method}>{method}</NativeSelectOption>
                              )}
                              {POLICY_METHODS.map((name) => (
                                <NativeSelectOption key={name} value={name}>
                                  {name}
                                </NativeSelectOption>
                              ))}
                            </NativeSelect>
                            <Input
                              className="h-7 min-w-40 font-mono text-xs md:text-xs"
                              value={c.request.path ?? ""}
                              disabled={disabled}
                              spellCheck={false}
                              autoComplete="off"
                              placeholder="/api/projects"
                              aria-label={swt("authz.mxPathAria", { p0: index + 1 })}
                              onChange={(event) => setRequest(index, { path: event.target.value })}
                            />
                          </div>
                          {write && (
                            <label
                              htmlFor={`authz-mx-write-${index}`}
                              className={cn(
                                "flex w-fit items-center gap-1.5 text-xs",
                                c.allow_write ? "text-muted-foreground" : "text-amber-700 dark:text-amber-400",
                              )}
                            >
                              <Checkbox
                                id={`authz-mx-write-${index}`}
                                checked={c.allow_write === true}
                                disabled={disabled}
                                onCheckedChange={(value) => {
                                  const next = { ...c };
                                  if (value === true) next.allow_write = true;
                                  else delete next.allow_write;
                                  setCase(index, next);
                                }}
                              />
                              {swt("authz.mxWrite")}
                            </label>
                          )}
                          {c.deny_all && (
                            <span className="text-muted-foreground text-xs">{swt("authz.cdDenyAll")}</span>
                          )}
                        </div>
                      </TableCell>
                      {doc.personas.map((p) => (
                        <TableCell key={p.key} className="align-top">
                          <VerdictCell
                            expectation={expectationOf(c, p.key)}
                            denyStatus={isAnonymous(p.key) ? 401 : 403}
                            role={roleOf(p)}
                            request={label}
                            disabled={disabled}
                            onChange={(next) => setExpectation(index, p.key, next)}
                          />
                        </TableCell>
                      ))}
                      <TableCell className="align-top">
                        <div className="flex justify-end gap-1">
                          <Button
                            type="button"
                            size="icon-sm"
                            variant="ghost"
                            disabled={disabled}
                            aria-label={swt("authz.mxDetailsAria", { p0: label })}
                            title={swt("authz.mxDetails")}
                            onClick={() => setDetailsIndex(index)}
                          >
                            <SlidersHorizontalIcon />
                          </Button>
                          <Button
                            type="button"
                            size="icon-sm"
                            variant="ghost"
                            disabled={disabled}
                            aria-label={swt("authz.mxRemoveRequestAria", { p0: label })}
                            title={swt("authz.mxRemoveRequest")}
                            onClick={() => onChange({ ...doc, cases: doc.cases.filter((_, i) => i !== index) })}
                          >
                            <Trash2Icon />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>

      <SignInDialog
        persona={signInPersona}
        role={signInPersona ? roleOf(signInPersona) : ""}
        account={signInPersona ? accountByKey.get(signInPersona.key) : undefined}
        others={formRoles.filter((p) => p.key !== signInKey).length}
        onClose={() => setSignInKey(null)}
        onApply={(signIn, path, texts, toOthers) => signInKey && applySignIn(signInKey, signIn, path, texts, toOthers)}
      />
      <CaseDialog
        item={detailsIndex === null ? null : (doc.cases[detailsIndex] ?? null)}
        personas={doc.personas.map((p) => ({ key: p.key, role: roleOf(p) }))}
        onClose={() => setDetailsIndex(null)}
        onApply={(next) => {
          if (detailsIndex !== null) setCase(detailsIndex, next);
          setDetailsIndex(null);
        }}
      />
    </div>
  );
}
