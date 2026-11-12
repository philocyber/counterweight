import type { NextRequest } from "next/server";
import { NextResponse } from "next/server";

const AUTH_PAGES = ["/login", "/setup"];

export function proxy(request: NextRequest) {
  const finish = (response: NextResponse) => {
    // Expire legacy credential cookies before the browser renders this page.
    if (request.cookies.has("counterweight_token")) response.cookies.delete("counterweight_token");
    return response;
  };
  // Mock mode bypasses real login and permits every page; client auth guards also allow entry.
  if (process.env.NEXT_PUBLIC_MOCK === "1") return finish(NextResponse.next());

  const { pathname } = request.nextUrl;
  // This is a routing hint, never an authorization credential. API access is
  // independently checked by the backend and client guards read localStorage.
  const sessionHint = request.cookies.get("counterweight_session")?.value === "1";
  const isAuthPage = AUTH_PAGES.some((p) => pathname === p || pathname.startsWith(`${p}/`));

  // Unauthenticated users redirect to login.
  if (!sessionHint && !isAuthPage) {
    return finish(NextResponse.redirect(new URL("/login", request.url)));
  }

  // Authenticated users visiting login/setup redirect to the app.
  if (sessionHint && isAuthPage) {
    return finish(NextResponse.redirect(new URL("/function/tasks", request.url)));
  }

  return finish(NextResponse.next());
}

export const config = {
  // Skip Next internals, API routes, favicon, and public static files such as images and fonts.
  matcher: [
    "/((?!_next/static|_next/image|favicon\\.ico|api/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|woff2?|ttf|otf)$).*)",
  ],
};
