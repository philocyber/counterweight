"use client";

import { useEffect, useRef, useState } from "react";

import { toast } from "sonner";

import { useI18n } from "@/i18n";
import { type DictationResult, mergeDictation } from "@/lib/speech-dictation";

interface DictationSession {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((event: { results: ArrayLike<DictationResult> }) => void) | null;
  onerror: ((event: { error: string }) => void) | null;
  onend: (() => void) | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
}

function recognitionConstructor(): (new () => DictationSession) | null {
  if (typeof window === "undefined") return null;
  const host = window as Window & {
    SpeechRecognition?: new () => DictationSession;
    webkitSpeechRecognition?: new () => DictationSession;
  };
  return host.SpeechRecognition ?? host.webkitSpeechRecognition ?? null;
}

// useDictation fills the composer from the browser speech recognizer.
// Stopping keeps the text so it can be edited. The caller submits that text
// with the same send action used for the keyboard.
export function useDictation(value: string, onChange: (next: string) => void, disabled: boolean) {
  const { t } = useI18n();
  const [recording, setRecording] = useState(false);
  const session = useRef<DictationSession | null>(null);
  const base = useRef(value);
  const latest = useRef(value);
  const onChangeRef = useRef(onChange);
  latest.current = value;
  onChangeRef.current = onChange;

  useEffect(() => {
    if (disabled && session.current) session.current.abort();
  }, [disabled]);

  useEffect(() => {
    return () => session.current?.abort();
  }, []);

  function finish(): string {
    const text = latest.current;
    const current = session.current;
    session.current = null;
    current?.stop();
    setRecording(false);
    return text;
  }

  function toggle() {
    if (disabled) return;
    if (session.current) {
      finish();
      return;
    }
    const Ctor = recognitionConstructor();
    if (!Ctor) {
      toast.error(t("interface.dictateUnavailable"));
      return;
    }
    const next = new Ctor();
    next.lang = navigator.language || "en-US";
    next.continuous = true;
    next.interimResults = true;
    base.current = value;
    latest.current = value;
    next.onresult = (event) => {
      const text = mergeDictation(base.current, event.results);
      latest.current = text;
      onChangeRef.current(text);
    };
    next.onerror = (event) => {
      if (event.error === "aborted" || event.error === "no-speech") return;
      if (event.error === "not-allowed" || event.error === "service-not-allowed") {
        toast.error(t("interface.dictateDenied"));
        return;
      }
      toast.error(t("interface.dictateError", { p0: event.error }));
    };
    next.onend = () => {
      if (session.current !== next) return;
      session.current = null;
      setRecording(false);
    };
    try {
      next.start();
    } catch (error) {
      toast.error(t("interface.dictateError", { p0: (error as Error).message }));
      return;
    }
    session.current = next;
    setRecording(true);
  }

  return { recording, toggle, finish };
}
