import type { MessageKey } from "../i18n/catalog";
import type { ExecutionBackendStatus, ExecutionStatus } from "./types";

// The three global execution isolation modes, in the order they are offered.
export type ExecutionMode = "off" | "confined-files" | "container";
export const EXECUTION_MODES: ExecutionMode[] = ["off", "confined-files", "container"];

// Tool families the server reports as unavailable in container mode, with the i18n key of their label. An
// unknown family is shown with the key the server sent.
const FAMILY_LABEL_KEYS: Record<string, MessageKey> = {
  "background-tasks": "execution.familyBackgroundTasks",
  "interactive-shells": "execution.familyInteractiveShells",
  "web-fetch": "execution.familyWebFetch",
  "custom-command-tools": "execution.familyCustomCommandTools",
  "custom-script-tools": "execution.familyCustomScriptTools",
  "stdio-mcp": "execution.familyStdioMcp",
  "remote-mcp": "execution.familyRemoteMcp",
  "platform-management": "execution.familyPlatformManagement",
  "cross-task-orchestration": "execution.familyCrossTaskOrchestration",
};

// The server only lists the unavailable families while container mode is the selected one; for the other
// modes the card shows these known families so the limits are visible before choosing.
export const KNOWN_TOOL_FAMILIES = Object.keys(FAMILY_LABEL_KEYS);

// Label of a tool family in the console language, or the server's own key when it is not recognised.
export function familyLabel(key: string, t: (key: MessageKey) => string): string {
  const labelKey = FAMILY_LABEL_KEYS[key];
  return labelKey ? t(labelKey) : key;
}

// True when the server reports per-backend status (a build that knows container mode). An older server
// only reports the on/off setting.
export function hasModeInfo(status: ExecutionStatus): boolean {
  return Array.isArray(status.backends) && status.backends.length > 0;
}

// The mode currently stored on the server.
export function selectedMode(status: ExecutionStatus): ExecutionMode {
  if (
    status.selected_mode === "off" ||
    status.selected_mode === "confined-files" ||
    status.selected_mode === "container"
  )
    return status.selected_mode;
  return status.setting ? "confined-files" : "off";
}

export type OptionState = "ready" | "not-available" | "failed";

export interface ModeOption {
  mode: ExecutionMode;
  state: OptionState;
  reason: string;
  fix: string;
  image?: string;
  imageDigest?: string;
  // Only a ready backend can be chosen; the current selection is never offered as a change.
  selectable: boolean;
}

// The backend entry that implements a mode. "Off" runs on the host.
function backendFor(status: ExecutionStatus, mode: ExecutionMode): ExecutionBackendStatus | undefined {
  const name = mode === "off" ? "host" : mode;
  return status.backends?.find((b) => b.backend === name);
}

export function modeOption(status: ExecutionStatus, mode: ExecutionMode): ModeOption {
  const backend = backendFor(status, mode);
  // A mode the server does not list is not offered as available.
  let state: OptionState = "not-available";
  if (backend?.status === "ready") state = "ready";
  else if (backend?.status === "failed") state = "failed";
  return {
    mode,
    state,
    reason: backend?.reason ?? "",
    fix: backend?.fix ?? "",
    image: backend?.image || undefined,
    imageDigest: backend?.image_digest || undefined,
    selectable: state === "ready" && selectedMode(status) !== mode,
  };
}

// A fixing step that is a shell command is shown in a copyable code block; anything else is a sentence.
export function isCommand(fix: string): boolean {
  return /^docker\s/.test(fix.trim());
}

// "sha256:" plus the first 12 hex characters of an image ID, which is enough to recognise an image.
export function shortDigest(digest: string): string {
  const m = /^(sha256:)?([0-9a-f]+)$/i.exec(digest.trim());
  if (!m) return digest;
  return `sha256:${m[2].slice(0, 12)}`;
}
