// Browser dictation stays in the composer. The active LLM runs only when the
// resulting text is sent, the same way a typed message is sent.

export interface DictationAlternative {
  transcript: string;
}

export interface DictationResult {
  isFinal: boolean;
  0?: DictationAlternative;
}

// mergeDictation joins the phrases the recognizer has emitted onto the text
// that was already in the field when recording started.
export function mergeDictation(base: string, results: ArrayLike<DictationResult>): string {
  let spoken = "";
  for (let i = 0; i < results.length; i++) spoken += results[i]?.[0]?.transcript ?? "";
  spoken = spoken.replace(/\s+/g, " ").trim();
  const prefix = base.replace(/\s+$/u, "");
  if (!spoken) return base;
  if (!prefix) return spoken;
  return `${prefix} ${spoken}`;
}
