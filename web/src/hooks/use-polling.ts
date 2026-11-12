"use client";

import * as React from "react";

// A long-running console is often left open in a background tab, where raw
// setInterval keeps hitting the backend every few seconds for data nobody is
// looking at. These helpers pause polling while the tab is hidden and resume —
// with one immediate catch-up call — when it becomes visible again.

// pollWhileVisible is a drop-in replacement for the setInterval/clearInterval
// pair inside an effect. It does NOT fire `fn` on initial start (the caller's
// existing `void load()` already does that), so surrounding guards such as a
// local `alive` flag and the initial load are preserved exactly; it only adds
// an immediate call when the tab returns to the foreground. Returns a stop
// function to call from the effect's cleanup in place of clearInterval.
export function pollWhileVisible(fn: () => void | Promise<void>, intervalMs: number): () => void {
  const hidden = () => typeof document !== "undefined" && document.hidden;
  let timer: ReturnType<typeof setInterval> | undefined;
  const start = () => {
    if (timer === undefined) timer = setInterval(fn, intervalMs);
  };
  const stop = () => {
    if (timer !== undefined) {
      clearInterval(timer);
      timer = undefined;
    }
  };
  const onVisibility = () => {
    if (hidden()) {
      stop();
    } else {
      fn(); // catch up immediately on resume
      start();
    }
  };
  if (!hidden()) start();
  if (typeof document !== "undefined") document.addEventListener("visibilitychange", onVisibility);
  return () => {
    if (typeof document !== "undefined") document.removeEventListener("visibilitychange", onVisibility);
    stop();
  };
}

// usePolling owns the whole effect for the simple case: run `fn` immediately,
// then poll it while visible. `fn` is kept in a ref so passing a fresh closure
// each render does not reset the interval; list real restart triggers in `deps`.
export function usePolling(
  fn: () => void | Promise<void>,
  intervalMs: number,
  deps: React.DependencyList = [],
  enabled = true,
) {
  const saved = React.useRef(fn);
  saved.current = fn;

  // biome-ignore lint/correctness/useExhaustiveDependencies: deps are the caller's explicit restart triggers.
  React.useEffect(() => {
    if (!enabled) return;
    const tick = () => saved.current();
    tick(); // initial run
    return pollWhileVisible(tick, intervalMs);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intervalMs, enabled, ...deps]);
}
