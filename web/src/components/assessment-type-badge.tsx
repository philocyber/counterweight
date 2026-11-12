"use client";

import { CodeIcon, GlobeIcon, type LucideIcon, NetworkIcon, ShapesIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { type MessageKey, useI18n } from "@/i18n";
import { assessmentTypeOf } from "@/lib/task-workspace";
import type { AssessmentType } from "@/lib/types";
import { cn } from "@/lib/utils";

// Display data for each assessment type, in the order the type picker shows them.
export const ASSESSMENT_TYPE_META: {
  value: AssessmentType;
  icon: LucideIcon;
  title: MessageKey;
  hint: MessageKey;
  short: MessageKey;
}[] = [
  {
    value: "web_app",
    icon: GlobeIcon,
    title: "taskform.typeWebApp",
    hint: "taskform.typeWebAppHint",
    short: "taskform.typeShortWebApp",
  },
  {
    value: "network",
    icon: NetworkIcon,
    title: "taskform.typeNetwork",
    hint: "taskform.typeNetworkHint",
    short: "taskform.typeShortNetwork",
  },
  {
    value: "code_review",
    icon: CodeIcon,
    title: "taskform.typeCodeReview",
    hint: "taskform.typeCodeReviewHint",
    short: "taskform.typeShortCodeReview",
  },
  {
    value: "general",
    icon: ShapesIcon,
    title: "taskform.typeGeneral",
    hint: "taskform.typeGeneralHint",
    short: "taskform.typeShortGeneral",
  },
];

export function assessmentTypeMeta(type: AssessmentType) {
  return ASSESSMENT_TYPE_META.find((item) => item.value === type) ?? ASSESSMENT_TYPE_META[3];
}

// AssessmentTypeBadge shows a type as an icon and a short label; the full name is the tooltip
// and the accessible text.
export function AssessmentTypeBadge({ type, className }: { type?: string | null; className?: string }) {
  "use no memo";
  const { t: swt } = useI18n();
  const meta = assessmentTypeMeta(assessmentTypeOf({ assessment_type: type ?? undefined }));
  const Icon = meta.icon;
  return (
    <Badge variant="outline" className={cn("shrink-0", className)} title={swt(meta.title)}>
      <Icon aria-hidden="true" />
      <span aria-hidden="true">{swt(meta.short)}</span>
      <span className="sr-only">{swt(meta.title)}</span>
    </Badge>
  );
}
