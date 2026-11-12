// Helpers for building API request paths from identifiers that may come from the URL bar or from
// stored data. Every dynamic path segment goes through seg() so a value can never add, remove or
// change a segment of the request path.

// seg encodes one path segment. Slashes, "?", "#" and "%" are percent-encoded by encodeURIComponent.
// Dot-only values are the one case it leaves alone, and a browser resolves "." and ".." (also when
// written as %2e) before sending the request, so their dots are double-encoded: the server decodes
// them once and sees a plain value that matches nothing, which ends in a 404.
export function seg(value: string | number): string {
  const encoded = encodeURIComponent(String(value));
  return /^\.+$/.test(encoded) ? encoded.replace(/\./g, "%252E") : encoded;
}

// segPath encodes a relative file path (such as "docs/readme.md"): each segment is encoded on its
// own and the separators between them are kept.
export function segPath(path: string): string {
  return path.split("/").map(seg).join("/");
}

// Identifier grammars used by the server; pages check URL-derived values against them before
// rendering anything that can change data.
export const AGENT_KEY_PATTERN = /^[a-z][a-z0-9_]*$/;
export const NUMERIC_ID_PATTERN = /^[1-9]\d*$/;

export function isAgentKey(value: string): boolean {
  return AGENT_KEY_PATTERN.test(value);
}

export function isNumericId(value: string): boolean {
  return NUMERIC_ID_PATTERN.test(value);
}
