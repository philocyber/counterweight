import { validateRef } from "./code-review-form.ts";
import type { ReviewChangeInput } from "./types";

// Client-side mirror of the server's rules for the change of a code review. It catches cheap mistakes
// early; the server stays the authority and its message is shown on failure.
export type ChangeKind = "repository" | "pull" | "range";

export interface ChangeDraft {
  kind: ChangeKind;
  pull: string;
  base: string;
  head: string;
}

export const EMPTY_CHANGE_DRAFT: ChangeDraft = { kind: "repository", pull: "", base: "", head: "" };

export type ChangeProblem =
  | "pullRequired"
  | "pullInvalid"
  | "baseRequired"
  | "baseInvalid"
  | "headRequired"
  | "headInvalid"
  | "repositoryHasRef";

const MAX_PULL_NUMBER = 1_000_000_000;

// validateChange returns the first problem with the draft, or null when it can be sent. A change review
// names its change, so a repository written as owner/name#ref is refused.
export function validateChange(draft: ChangeDraft, repository: string): ChangeProblem | null {
  if (draft.kind === "repository") return null;
  if (repository.includes("#")) return "repositoryHasRef";
  if (draft.kind === "pull") {
    const value = draft.pull.trim();
    if (value === "") return "pullRequired";
    const n = Number(value);
    return /^[0-9]{1,10}$/.test(value) && n >= 1 && n <= MAX_PULL_NUMBER ? null : "pullInvalid";
  }
  const base = draft.base.trim();
  const head = draft.head.trim();
  if (base === "") return "baseRequired";
  if (validateRef(base) !== null) return "baseInvalid";
  if (head === "") return "headRequired";
  if (validateRef(head) !== null) return "headInvalid";
  return null;
}

// changeInput builds the `change` field of the create request, or undefined for a whole-repository review.
export function changeInput(draft: ChangeDraft): ReviewChangeInput | undefined {
  if (draft.kind === "pull") return { pull_number: Number(draft.pull.trim()) };
  if (draft.kind === "range") return { base: draft.base.trim(), head: draft.head.trim() };
  return undefined;
}

// shortCommit shortens a commit id for display.
export function shortCommit(sha: string | undefined): string {
  return (sha ?? "").slice(0, 7);
}
