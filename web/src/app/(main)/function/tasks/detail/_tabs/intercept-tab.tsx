"use client";
import { ApprovalRecords } from "@/components/approval-records";
import { useI18n } from "@/i18n";

export function InterceptTab({ taskId }: { taskId: string }) {
  "use no memo";
  const { locale: swLocale } = useI18n();

  return <ApprovalRecords key={taskId} taskId={taskId} />;
}
