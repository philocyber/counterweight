// Assembles the per-namespace message modules into a flat English catalog.
// Keys are "<namespace>.<key>". i18n.test.mjs checks placeholder/tag parity
// against the interpolation fields each message declares.

import * as app from "./messages/app.ts";
import * as authz from "./messages/authz.ts";
import * as blocked from "./messages/blocked.ts";
import * as budget from "./messages/budget.ts";
import * as common from "./messages/common.ts";
import * as confirm from "./messages/confirm.ts";
import * as english from "./messages/english.ts";
import * as execution from "./messages/execution.ts";
import * as interfaceMessages from "./messages/interface.ts";
import * as jira from "./messages/jira.ts";
import * as models from "./messages/models.ts";
import * as nav from "./messages/nav.ts";
import * as onboarding from "./messages/onboarding.ts";
import * as providers from "./messages/providers.ts";
import * as prreview from "./messages/prreview.ts";
import * as report from "./messages/report.ts";
import * as scan from "./messages/scan.ts";
import * as settings from "./messages/settings.ts";
import * as states from "./messages/states.ts";
import * as taskdetail from "./messages/taskdetail.ts";
import * as taskform from "./messages/taskform.ts";
import * as tokens from "./messages/tokens.ts";

const namespaces = {
  providers,
  settings,
  english,
  interface: interfaceMessages,
  app,
  common,
  onboarding,
  taskform,
  confirm,
  taskdetail,
  states,
  jira,
  report,
  nav,
  models,
  execution,
  budget,
  scan,
  tokens,
  prreview,
  authz,
  blocked,
};

type Namespaces = typeof namespaces;
type NamespaceName = keyof Namespaces & string;

export type MessageKey = {
  [N in NamespaceName]: `${N}.${keyof Namespaces[N]["en"] & string}`;
}[NamespaceName];

export type Catalog = Record<MessageKey, string>;

function flatten(locale: "en" | "es"): Catalog {
  const out: Record<string, string> = {};
  for (const [ns, mod] of Object.entries(namespaces)) {
    const table = (mod as Record<string, Record<string, string>>)[locale];
    for (const [key, value] of Object.entries(mod.en as Record<string, string>)) {
      // Fall back to the English source for any key a translation omits so the
      // catalog stays complete by construction.
      out[`${ns}.${key}`] = table?.[key] ?? value;
    }
  }
  return out as Catalog;
}

export const catalogs = { en: flatten("en"), es: flatten("es") } as const;

export const namespaceNames = Object.keys(namespaces);
