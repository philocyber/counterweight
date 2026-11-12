"use client";

import { Badge } from "@/components/ui/badge";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useI18n } from "@/i18n";
import type { LLMProfile } from "@/lib/types";

import { ConnectWizard } from "./connect-wizard";
import { useProfileDraft } from "./profile-draft";
import { ProfileFullForm } from "./profile-form";

// Model-profile drawer: the guided "Connect a model" flow for new API-key profiles, the full form for editing
// an existing profile and for the ChatGPT / Codex sign-in.
export function ProfileSheet({
  profile,
  open,
  onOpenChange,
  onSaved,
  hasActiveProfile,
  initialSubscription = false,
}: {
  profile: LLMProfile | null; // Null means creation.
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSaved: (id: string) => void;
  hasActiveProfile: boolean;
  initialSubscription?: boolean;
}) {
  "use no memo";
  const { t } = useI18n();
  const d = useProfileDraft({ profile, open, onOpenChange, onSaved, initialSubscription });
  const guided = d.guided;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex flex-col gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:min-w-[420px] data-[side=right]:sm:max-w-xl"
      >
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            {guided
              ? t("models.connectTitle")
              : d.isNew
                ? t("interface.m1382")
                : t("interface.m1383", { p0: profile?.name })}
            {profile?.is_default && (
              <Badge variant="outline" className="border-amber-400/50 text-amber-700 dark:text-amber-400">
                {t("interface.m1384")}
              </Badge>
            )}
          </SheetTitle>
          <SheetDescription>
            {guided ? t("models.connectDescription") : d.isNew ? t("interface.m1385") : t("interface.m1386")}
          </SheetDescription>
        </SheetHeader>

        {guided ? <ConnectWizard d={d} hasActiveProfile={hasActiveProfile} /> : <ProfileFullForm d={d} />}
      </SheetContent>
    </Sheet>
  );
}
