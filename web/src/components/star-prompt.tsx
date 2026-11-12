"use client";

import * as React from "react";

import { StarIcon, XIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";
import { api } from "@/lib/api";
import type { StarPrompt as StarPromptState } from "@/lib/types";

/**
 * StarPrompt is a small, one-time invitation to star the project on GitHub. The
 * server decides when it is due (only after the installation has produced
 * results) and remembers the answer. With a stored GitHub token the star is set
 * in one click; otherwise the button opens the repository page.
 */
export function StarPrompt() {
  const { t: swt } = useI18n();
  const [prompt, setPrompt] = React.useState<StarPromptState | null>(null);
  const [busy, setBusy] = React.useState(false);
  // Set after a direct attempt failed: the card then offers the repository link.
  const [fallback, setFallback] = React.useState(false);

  React.useEffect(() => {
    api
      .starPrompt()
      .then((p) => setPrompt(p.show ? p : null))
      .catch(() => setPrompt(null));
  }, []);

  if (!prompt) return null;

  const answer = (action: "later" | "dismiss") => {
    setPrompt(null);
    api.answerStarPrompt(action).catch(() => undefined);
  };

  const star = async () => {
    setBusy(true);
    try {
      const out = await api.answerStarPrompt("star");
      if (out.starred) {
        toast.success(swt("onboarding.starThanks"));
        setPrompt(null);
        return;
      }
    } catch {
      // Fall through to the repository link.
    } finally {
      setBusy(false);
    }
    setFallback(true);
  };

  const link = !prompt.direct || fallback;

  return (
    <aside
      aria-label={swt("onboarding.starTitle")}
      className="fixed right-4 bottom-4 z-40 w-[min(22rem,calc(100vw-2rem))] rounded-lg border bg-card p-4 text-card-foreground shadow-lg"
    >
      <button
        type="button"
        onClick={() => answer("dismiss")}
        aria-label={swt("onboarding.starDismiss")}
        title={swt("onboarding.starDismiss")}
        className="absolute top-2 right-2 rounded-md p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        <XIcon className="size-4" />
      </button>
      <p className="pr-6 text-sm font-medium">{swt("onboarding.starTitle")}</p>
      <p className="mt-1 text-sm text-muted-foreground">
        {fallback ? swt("onboarding.starOpenRepo") : swt("onboarding.starBody")}
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {link ? (
          <Button size="sm" asChild>
            <a
              href={prompt.url}
              target="_blank"
              rel="noreferrer"
              onClick={() => {
                setPrompt(null);
                if (!fallback) api.answerStarPrompt("star").catch(() => undefined);
              }}
            >
              <StarIcon /> {swt("onboarding.starAction")}
            </a>
          </Button>
        ) : (
          <Button size="sm" disabled={busy} onClick={() => void star()}>
            <StarIcon /> {swt("onboarding.starAction")}
          </Button>
        )}
        <Button size="sm" variant="ghost" onClick={() => answer("later")}>
          {swt("onboarding.starLater")}
        </Button>
      </div>
    </aside>
  );
}
