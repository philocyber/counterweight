"use client";

import { useEffect } from "react";

import { ErrorState } from "@/components/error-state";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/i18n";

export default function MainError({ error, retry }: { error: unknown; retry: () => void }) {
  "use no memo";
  const { t: swt } = useI18n();

  useEffect(() => {
    console.error(error);
  }, [error]);

  return (
    <div className="flex flex-1 items-center justify-center">
      <ErrorState
        title={swt("states.errorBoundaryTitle")}
        detail={swt("states.errorBoundaryDetail")}
        onRetry={retry}
        actions={
          <Button size="sm" variant="ghost" onClick={() => window.location.reload()}>
            {swt("states.reloadPage")}
          </Button>
        }
      />
    </div>
  );
}
