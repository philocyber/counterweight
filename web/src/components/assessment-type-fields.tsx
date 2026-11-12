"use client";

import * as React from "react";

import { ASSESSMENT_TYPE_META } from "@/components/assessment-type-badge";
import { Field, FieldDescription, FieldError, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { type MessageKey, useI18n } from "@/i18n";
import {
  CODE_REVIEW_MODES,
  type RefProblem,
  type RepositoryProblem,
  validateRef,
  validateRepository,
} from "@/lib/code-review-form";
import { type ChangeDraft, type ChangeKind, type ChangeProblem, validateChange } from "@/lib/review-change";
import type { AssessmentType, CodeReviewMode } from "@/lib/types";
import { cn } from "@/lib/utils";

// AssessmentTypePicker is a required single choice shown as cards, with radio-group semantics.
export function AssessmentTypePicker({
  value,
  onValueChange,
  disabled,
  idPrefix = "assessment-type",
}: {
  value: AssessmentType;
  onValueChange: (value: AssessmentType) => void;
  disabled?: boolean;
  idPrefix?: string;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const legendId = `${idPrefix}-legend`;
  return (
    <FieldSet>
      <FieldLegend id={legendId} variant="label">
        {swt("taskform.typeLegend")}
        <span aria-hidden="true" className="text-destructive">
          *
        </span>
        <span className="sr-only">({swt("taskform.requiredField")})</span>
      </FieldLegend>
      <RadioGroup
        value={value}
        onValueChange={(next) => onValueChange(next as AssessmentType)}
        disabled={disabled}
        required
        aria-labelledby={legendId}
        className="grid-cols-1 gap-2 sm:grid-cols-2"
      >
        {ASSESSMENT_TYPE_META.map((item) => {
          const Icon = item.icon;
          const id = `${idPrefix}-${item.value}`;
          return (
            <label
              key={item.value}
              htmlFor={id}
              className={cn(
                "flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-accent/50",
                "has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                "has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
                disabled && "cursor-not-allowed opacity-60 hover:bg-transparent",
              )}
            >
              <Icon aria-hidden="true" className="mt-0.5 size-5 shrink-0 text-muted-foreground" />
              <span className="grid min-w-0 flex-1 gap-0.5">
                <span className="font-medium text-sm leading-snug">{swt(item.title)}</span>
                <span className="text-muted-foreground text-xs leading-snug">{swt(item.hint)}</span>
              </span>
              <RadioGroupItem id={id} value={item.value} className="mt-0.5" />
            </label>
          );
        })}
      </RadioGroup>
    </FieldSet>
  );
}

const REPOSITORY_PROBLEMS: Record<RepositoryProblem, MessageKey> = {
  required: "taskform.repositoryRequired",
  whitespace: "taskform.repositoryWhitespace",
  scheme: "taskform.repositoryScheme",
  credentials: "taskform.repositoryCredentials",
  host: "taskform.repositoryHost",
  format: "taskform.repositoryFormat",
};

const REF_PROBLEMS: Record<RefProblem, MessageKey> = { format: "taskform.refInvalid" };

const DEPTH_LABELS: Record<CodeReviewMode, { title: MessageKey; hint: MessageKey }> = {
  light: { title: "taskform.depthLight", hint: "taskform.depthLightHint" },
  balanced: { title: "taskform.depthBalanced", hint: "taskform.depthBalancedHint" },
  comprehensive: { title: "taskform.depthComprehensive", hint: "taskform.depthComprehensiveHint" },
};

// repositoryError returns the message key for an invalid repository, or null when it is acceptable.
export function repositoryError(value: string): MessageKey | null {
  const problem = validateRepository(value);
  return problem ? REPOSITORY_PROBLEMS[problem] : null;
}

// refError returns the message key for an invalid ref, or null when it is acceptable.
export function refError(value: string): MessageKey | null {
  const problem = validateRef(value);
  return problem ? REF_PROBLEMS[problem] : null;
}

const CHANGE_PROBLEMS: Record<ChangeProblem, MessageKey> = {
  pullRequired: "prreview.pullRequired",
  pullInvalid: "prreview.pullInvalid",
  baseRequired: "prreview.baseRequired",
  baseInvalid: "prreview.baseInvalid",
  headRequired: "prreview.headRequired",
  headInvalid: "prreview.headInvalid",
  repositoryHasRef: "prreview.repositoryHasRef",
};

const CHANGE_KINDS: { value: ChangeKind; title: MessageKey; hint: MessageKey }[] = [
  { value: "repository", title: "prreview.scopeRepository", hint: "prreview.scopeRepositoryHint" },
  { value: "pull", title: "prreview.scopePull", hint: "prreview.scopePullHint" },
  { value: "range", title: "prreview.scopeRange", hint: "prreview.scopeRangeHint" },
];

// changeErrorKey returns the message key for an invalid change, or null when it can be sent.
export function changeErrorKey(draft: ChangeDraft, repository: string): MessageKey | null {
  const problem = validateChange(draft, repository);
  return problem ? CHANGE_PROBLEMS[problem] : null;
}

// ChangeScopeFields is the optional "Review a change" choice of a code review: the whole repository
// (default), one pull request, or the difference between a base and a head.
function ChangeScopeFields({
  repository,
  value,
  onChange,
  disabled,
  idPrefix,
}: {
  repository: string;
  value: ChangeDraft;
  onChange: (value: ChangeDraft) => void;
  disabled?: boolean;
  idPrefix: string;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [touched, setTouched] = React.useState(false);
  const problem = validateChange(value, repository);
  const showError = touched && problem !== null;
  const errorId = `${idPrefix}-change-error`;
  const bad = (...problems: ChangeProblem[]) => showError && problem !== null && problems.includes(problem);
  return (
    <FieldSet>
      <FieldLegend variant="label">
        {swt("prreview.scopeLegend")}{" "}
        <span className="font-normal text-muted-foreground">{swt("prreview.scopeOptional")}</span>
      </FieldLegend>
      <RadioGroup
        value={value.kind}
        onValueChange={(next) => onChange({ ...value, kind: next as ChangeKind })}
        disabled={disabled}
        className="gap-2"
      >
        {CHANGE_KINDS.map((item) => {
          const id = `${idPrefix}-change-${item.value}`;
          return (
            <label
              key={item.value}
              htmlFor={id}
              className={cn(
                "flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-accent/50",
                "has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                "has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
                disabled && "cursor-not-allowed opacity-60 hover:bg-transparent",
              )}
            >
              <RadioGroupItem id={id} value={item.value} className="mt-0.5" />
              <span className="grid min-w-0 flex-1 gap-0.5">
                <span className="font-medium text-sm leading-snug">{swt(item.title)}</span>
                <span className="text-muted-foreground text-xs leading-snug">{swt(item.hint)}</span>
              </span>
            </label>
          );
        })}
      </RadioGroup>
      {value.kind === "pull" && (
        <Field data-invalid={bad("pullRequired", "pullInvalid")}>
          <FieldLabel htmlFor={`${idPrefix}-change-pull`}>{swt("prreview.pullLabel")}</FieldLabel>
          <Input
            id={`${idPrefix}-change-pull`}
            value={value.pull}
            disabled={disabled}
            inputMode="numeric"
            placeholder={swt("prreview.pullPlaceholder")}
            autoComplete="off"
            spellCheck={false}
            className="w-40 max-w-full"
            aria-invalid={bad("pullRequired", "pullInvalid")}
            aria-describedby={showError ? errorId : undefined}
            onChange={(event) => onChange({ ...value, pull: event.target.value })}
            onBlur={() => setTouched(true)}
          />
          <FieldDescription>{swt("prreview.pullHint")}</FieldDescription>
        </Field>
      )}
      {value.kind === "range" && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field data-invalid={bad("baseRequired", "baseInvalid")}>
            <FieldLabel htmlFor={`${idPrefix}-change-base`}>{swt("prreview.baseLabel")}</FieldLabel>
            <Input
              id={`${idPrefix}-change-base`}
              value={value.base}
              disabled={disabled}
              placeholder={swt("prreview.basePlaceholder")}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={bad("baseRequired", "baseInvalid")}
              aria-describedby={showError ? errorId : undefined}
              onChange={(event) => onChange({ ...value, base: event.target.value })}
              onBlur={() => setTouched(true)}
            />
            <FieldDescription>{swt("prreview.baseHint")}</FieldDescription>
          </Field>
          <Field data-invalid={bad("headRequired", "headInvalid")}>
            <FieldLabel htmlFor={`${idPrefix}-change-head`}>{swt("prreview.headLabel")}</FieldLabel>
            <Input
              id={`${idPrefix}-change-head`}
              value={value.head}
              disabled={disabled}
              placeholder={swt("prreview.headPlaceholder")}
              autoComplete="off"
              spellCheck={false}
              aria-invalid={bad("headRequired", "headInvalid")}
              aria-describedby={showError ? errorId : undefined}
              onChange={(event) => onChange({ ...value, head: event.target.value })}
              onBlur={() => setTouched(true)}
            />
            <FieldDescription>{swt("prreview.headHint")}</FieldDescription>
          </Field>
        </div>
      )}
      {showError && problem && <FieldError id={errorId}>{swt(CHANGE_PROBLEMS[problem])}</FieldError>}
    </FieldSet>
  );
}

// CodeReviewFields holds the repository, ref and depth inputs shared by the new-task form and the
// change-type dialog. Errors appear once a field was touched; the server still has the last word.
// Passing `change` and `onChangeChange` adds the optional "Review a change" choice; a change review
// names its change, so the branch or commit field is then not shown.
export function CodeReviewFields({
  repository,
  onRepositoryChange,
  refValue,
  onRefChange,
  mode,
  onModeChange,
  disabled,
  idPrefix = "code-review",
  showDepth = true,
  change,
  onChangeChange,
}: {
  repository: string;
  onRepositoryChange: (value: string) => void;
  refValue: string;
  onRefChange: (value: string) => void;
  mode: CodeReviewMode;
  onModeChange: (value: CodeReviewMode) => void;
  disabled?: boolean;
  idPrefix?: string;
  showDepth?: boolean;
  change?: ChangeDraft;
  onChangeChange?: (value: ChangeDraft) => void;
}) {
  "use no memo";
  const { t: swt } = useI18n();
  const [touched, setTouched] = React.useState({ repository: false, ref: false });
  const repositoryProblem = repositoryError(repository);
  const refProblem = refError(refValue);
  const showRepositoryError = touched.repository && repositoryProblem !== null;
  const showRefError = touched.ref && refProblem !== null;
  const changeScoped = change !== undefined && change.kind !== "repository";
  return (
    <>
      <Field data-invalid={showRepositoryError}>
        <FieldLabel htmlFor={`${idPrefix}-repository`}>
          {swt("taskform.repositoryLabel")}
          <span aria-hidden="true" className="text-destructive">
            *
          </span>
          <span className="sr-only">({swt("taskform.requiredField")})</span>
        </FieldLabel>
        <Input
          id={`${idPrefix}-repository`}
          value={repository}
          disabled={disabled}
          placeholder={swt("taskform.repositoryPlaceholder")}
          autoComplete="off"
          spellCheck={false}
          aria-required="true"
          aria-invalid={showRepositoryError}
          aria-describedby={showRepositoryError ? `${idPrefix}-repository-error` : undefined}
          onChange={(event) => onRepositoryChange(event.target.value)}
          onBlur={() => setTouched((current) => ({ ...current, repository: true }))}
        />
        <FieldDescription>{swt("taskform.repositoryHint")}</FieldDescription>
        {showRepositoryError && repositoryProblem && (
          <FieldError id={`${idPrefix}-repository-error`}>{swt(repositoryProblem)}</FieldError>
        )}
      </Field>
      {!changeScoped && (
        <Field data-invalid={showRefError}>
          <FieldLabel htmlFor={`${idPrefix}-ref`}>{swt("taskform.refLabel")}</FieldLabel>
          <Input
            id={`${idPrefix}-ref`}
            value={refValue}
            disabled={disabled}
            placeholder={swt("taskform.refPlaceholder")}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={showRefError}
            aria-describedby={showRefError ? `${idPrefix}-ref-error` : undefined}
            onChange={(event) => onRefChange(event.target.value)}
            onBlur={() => setTouched((current) => ({ ...current, ref: true }))}
          />
          <FieldDescription>{swt("taskform.refHint")}</FieldDescription>
          {showRefError && refProblem && <FieldError id={`${idPrefix}-ref-error`}>{swt(refProblem)}</FieldError>}
        </Field>
      )}
      {change && onChangeChange && (
        <ChangeScopeFields
          repository={repository}
          value={change}
          onChange={onChangeChange}
          disabled={disabled}
          idPrefix={idPrefix}
        />
      )}
      {showDepth && (
        <FieldSet>
          <FieldLegend variant="label">{swt("taskform.depthLabel")}</FieldLegend>
          <RadioGroup
            value={mode}
            onValueChange={(next) => onModeChange(next as CodeReviewMode)}
            disabled={disabled}
            className="gap-2"
          >
            {CODE_REVIEW_MODES.map((item) => {
              const id = `${idPrefix}-depth-${item}`;
              return (
                <label
                  key={item}
                  htmlFor={id}
                  className={cn(
                    "flex min-w-0 cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-accent/50",
                    "has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                    "has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
                    disabled && "cursor-not-allowed opacity-60 hover:bg-transparent",
                  )}
                >
                  <RadioGroupItem id={id} value={item} className="mt-0.5" />
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="font-medium text-sm leading-snug">{swt(DEPTH_LABELS[item].title)}</span>
                    <span className="text-muted-foreground text-xs leading-snug">{swt(DEPTH_LABELS[item].hint)}</span>
                  </span>
                </label>
              );
            })}
          </RadioGroup>
        </FieldSet>
      )}
    </>
  );
}
