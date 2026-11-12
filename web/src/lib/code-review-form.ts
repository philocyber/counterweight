import type { CodeReviewMode } from "./types";

// Client-side mirror of the server's repository and ref rules for code reviews. It only catches
// the cheap mistakes early; the server stays the authority and its message is shown on failure.
export type RepositoryProblem = "required" | "whitespace" | "scheme" | "credentials" | "host" | "format";
export type RefProblem = "format";

export const CODE_REVIEW_MODES: readonly CodeReviewMode[] = ["light", "balanced", "comprehensive"];

const NAME_SEGMENT = /^[A-Za-z0-9_][A-Za-z0-9_.-]*$/;
const REF_CHARS = /^[A-Za-z0-9_./-]+$/;
const GITHUB_HOSTS = new Set(["github.com", "www.github.com"]);

// validateRef accepts an empty ref (default branch) or a branch, tag or commit name.
export function validateRef(ref: string): RefProblem | null {
  const value = ref.trim();
  if (value === "") return null;
  if (value.length > 255 || value.startsWith("-") || value.includes("..") || !REF_CHARS.test(value)) return "format";
  return null;
}

function validSegment(value: string): boolean {
  return value.length <= 100 && NAME_SEGMENT.test(value);
}

// validateRepository checks an owner/name or GitHub URL, with an optional "#ref" suffix.
export function validateRepository(input: string): RepositoryProblem | null {
  const value = input.trim();
  if (value === "") return "required";
  if (/\s/.test(value)) return "whitespace";
  const hash = value.indexOf("#");
  const base = hash >= 0 ? value.slice(0, hash) : value;
  if (hash >= 0 && validateRef(value.slice(hash + 1)) !== null) return "format";
  if (hash >= 0 && value.slice(hash + 1) === "") return "format";

  let path = base;
  const scheme = /^([A-Za-z][A-Za-z0-9+.-]*):\/\//.exec(base);
  const hostFirst = /^(?:www\.)?github\.com\//i.test(base);
  if (scheme || hostFirst) {
    if (scheme && scheme[1].toLowerCase() !== "https") return "scheme";
    const rest = scheme ? base.slice(scheme[0].length) : base;
    const slash = rest.indexOf("/");
    const authority = slash >= 0 ? rest.slice(0, slash) : rest;
    if (authority.includes("@")) return "credentials";
    if (!GITHUB_HOSTS.has(authority.toLowerCase())) return "host";
    path = slash >= 0 ? rest.slice(slash + 1) : "";
  } else if (base.includes("@") || base.includes(":")) {
    return "credentials";
  }

  const parts = path.split("/");
  if (parts.length > 2 && parts[parts.length - 1] === "") parts.pop();
  const isURL = scheme !== null || hostFirst;
  // owner/name only for the short form; a URL may add /tree/<ref> or a trailing slash.
  if (parts.length < 2 || (!isURL && parts.length !== 2)) return "format";
  const name = parts[1].replace(/\.git$/, "");
  if (!validSegment(parts[0]) || !validSegment(name)) return "format";
  if (parts.length > 2 && !(parts[2] === "tree" && parts.length > 3)) return "format";
  return null;
}

// Template presets keep the ref inside the repository string as "owner/name#ref".
export function joinRepositoryRef(repository: string, ref: string): string {
  const repo = repository.trim();
  const r = ref.trim();
  return r && !repo.includes("#") ? `${repo}#${r}` : repo;
}

export function splitRepositoryRef(value: string): { repository: string; ref: string } {
  const hash = value.indexOf("#");
  return hash < 0 ? { repository: value, ref: "" } : { repository: value.slice(0, hash), ref: value.slice(hash + 1) };
}

// Preset text used when a code-review template has no operator guidance, because templates always
// need a goal. It is treated as empty again when the template is applied.
export const CODE_REVIEW_TEMPLATE_GOAL = "Review the repository for security weaknesses.";

export function codeReviewTemplateDescription(repository: string): string {
  return repository ? `Code review: ${repository}` : "";
}
