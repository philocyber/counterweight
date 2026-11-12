import { translate } from "../i18n/runtime.ts";

// Wire labels must stay stable for backend compatibility and existing user messages.
export const mentionKinds = [
  {
    kind: "finding",
    get label() {
      return translate("interface.m0526");
    },
    wire: "finding",
    alias: "finding",
  },
  {
    kind: "asset",
    get label() {
      return translate("interface.m0222");
    },
    wire: "asset",
    alias: "asset",
  },
  {
    kind: "company",
    get label() {
      return translate("interface.m0217");
    },
    wire: "company",
    alias: "company",
  },
  {
    kind: "endpoint",
    get label() {
      return translate("interface.m0218");
    },
    wire: "endpoint",
    alias: "api",
  },
  { kind: "ip", label: "IP", wire: "IP", alias: "ip" },
  {
    kind: "app",
    get label() {
      return translate("interface.m0144");
    },
    wire: "app",
    alias: "app",
  },
  {
    kind: "root_domain",
    get label() {
      return translate("interface.m0232");
    },
    wire: "root_domain",
    alias: "domain",
  },
  {
    kind: "subdomain",
    get label() {
      return translate("interface.m0143");
    },
    wire: "subdomain",
    alias: "subdomain",
  },
  {
    kind: "service",
    get label() {
      return translate("interface.m0145");
    },
    wire: "service",
    alias: "service",
  },
] as const;

export type MentionKind = (typeof mentionKinds)[number]["kind"];
export interface ChatMention {
  kind: MentionKind;
  id: number;
  label: string;
  description: string;
}

export function activeMention(value: string, caret: number) {
  const before = value.slice(0, caret);
  const start = before.lastIndexOf("@");
  if (start < 0 || (start > 0 && /[\w.+/-]/.test(before[start - 1]))) return null;
  const query = before.slice(start + 1);
  if (/[[\]\r\n@]/.test(query) || query.length > 220) return null;
  return { start, end: caret, query };
}

export function mentionSearch(query: string) {
  const text = query.trimStart().toLowerCase();
  for (const item of mentionKinds) {
    for (const alias of [item.label.toLowerCase(), item.wire.toLowerCase(), item.alias]) {
      if (text === alias || text.startsWith(`${alias} `) || (/[^a-z]/.test(alias) && text.startsWith(alias))) {
        return { kind: item.kind, query: query.trimStart().slice(alias.length).trim(), categories: [] };
      }
    }
  }
  const categories = mentionKinds.filter(
    (item) =>
      item.label.toLowerCase().startsWith(text) ||
      item.wire.toLowerCase().startsWith(text) ||
      item.alias.startsWith(text),
  );
  return { kind: "" as const, query: query.trim(), categories };
}

export function mentionToken(item: ChatMention) {
  const kind = mentionKinds.find((entry) => entry.kind === item.kind)?.wire ?? "asset";
  const label = item.label
    .replace(/[[\]]/g, (char) => (char === "[" ? "(" : ")"))
    .replace(/\s+/g, " ")
    .slice(0, 100);
  return `@[${kind}#${item.id} ${label}]`;
}

export function selectedMentions(value: string) {
  return [
    ...value.matchAll(
      /@\[(finding|asset|company|endpoint|IP|app|root_domain|subdomain|service)#([0-9]+)(?: ([^\]\r\n]*))?\]/g,
    ),
  ].map((match) => ({
    token: match[0],
    label: `${mentionKinds.find((item) => item.wire === match[1])?.label ?? match[1]} #${match[2]}${match[3] ? ` · ${match[3]}` : ""}`,
    start: match.index,
  }));
}
