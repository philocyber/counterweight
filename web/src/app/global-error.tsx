"use client";

import { useEffect } from "react";

import { DEFAULT_LOCALE } from "@/i18n/config";
import { readStoredLocale, translateIn } from "@/i18n/runtime";

// Last-resort boundary: it replaces the root layout, so it cannot use the app shell, providers or
// stylesheets. Markup and styles are self-contained.
const STYLES = `
.cw-ge{display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0;padding:24px;
font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:#fff;color:#171717;text-align:center}
.cw-ge h1{margin:0 0 8px;font-size:18px;font-weight:600}
.cw-ge p{margin:0 auto 20px;max-width:420px;font-size:14px;line-height:1.5;color:#525252}
.cw-ge-actions{display:flex;flex-wrap:wrap;justify-content:center;gap:8px}
.cw-ge button{font:inherit;font-size:14px;padding:8px 14px;border-radius:8px;cursor:pointer;
border:1px solid #d4d4d4;background:#fff;color:#171717}
.cw-ge button:hover{background:#f5f5f5}
@media (prefers-color-scheme:dark){
.cw-ge{background:#0a0a0a;color:#fafafa}
.cw-ge p{color:#a3a3a3}
.cw-ge button{background:#171717;color:#fafafa;border-color:#404040}
.cw-ge button:hover{background:#262626}}
`;

export default function GlobalError({ error, retry }: { error: unknown; retry: () => void }) {
  useEffect(() => {
    console.error(error);
  }, [error]);

  const locale = readStoredLocale() ?? DEFAULT_LOCALE;

  return (
    <html lang={locale}>
      <body style={{ margin: 0 }}>
        <style>{STYLES}</style>
        <div className="cw-ge" role="alert">
          <div>
            <h1>{translateIn(locale, "states.errorBoundaryTitle")}</h1>
            <p>{translateIn(locale, "states.errorBoundaryDetail")}</p>
            <div className="cw-ge-actions">
              <button type="button" onClick={() => retry()}>
                {translateIn(locale, "states.tryAgain")}
              </button>
              <button type="button" onClick={() => window.location.reload()}>
                {translateIn(locale, "states.reloadPage")}
              </button>
            </div>
          </div>
        </div>
      </body>
    </html>
  );
}
