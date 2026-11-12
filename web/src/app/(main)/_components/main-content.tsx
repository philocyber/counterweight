"use client";

import type { ReactNode } from "react";

import { usePathname } from "next/navigation";

// Task and finding details supply their own header, tabs, and padding.
function isFullBleed(pathname: string) {
  const p = (() => {
    try {
      return decodeURIComponent(pathname);
    } catch {
      return pathname;
    }
  })();
  // Static export uses trailingSlash, so the task-list pathname is /function/tasks/.
  // Remove the trailing slash before prefix checks or the list is mistaken for details and loses its padding.
  const normalized = p.replace(/\/+$/, "");
  return normalized.startsWith("/function/tasks/") || normalized.startsWith("/function/findings/");
}

export function MainContent({ children }: { children: ReactNode }) {
  "use no memo";
  const pathname = usePathname();

  if (isFullBleed(pathname)) {
    return <>{children}</>;
  }

  return (
    <div
      data-console="workspace"
      className="min-h-0 min-w-0 flex-1 overflow-x-clip p-4 has-data-[content-padding=false]:p-0 md:p-6 md:has-data-[content-padding=false]:p-0"
    >
      {children}
    </div>
  );
}
