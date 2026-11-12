#!/usr/bin/env python3
"""
spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "k=v; k2=v2"] [--max 200] [--depth 4]

Reference template, not a universal tool. Adapt --exclude, cookies, depth/max and same-origin rules before use.

Fallback for NON-SPA targets (traditional server-rendered MPAs: Django/Rails/PHP/
JSP, classic admin panels). When there is no JS endpoint bundle, the API surface
lives in HTML <form action>, <a href>, and inline-JS ajax urls. This BFS-crawls
the same origin and extracts:
  - forms.txt : METHOD action  [param1, param2, ...]   (the real "endpoints")
  - links.txt : every same-origin URL reached
  - api_inline.txt : url-ish strings found in inline <script> / onclick (fetch/ajax)

Stdlib only. Same-origin, bounded, polite. Provide --cookie for an authed crawl.
"""
import sys, os, re, argparse, urllib.request, urllib.parse, time
from html.parser import HTMLParser
from collections import deque
from response_budget import ResponseBudget, ResponseLimitError, add_budget_arguments, budget_from_args
from tls_policy import DEFAULT_TLS, add_tls_arguments, tls_from_args, report_tls_failure

UA = "Mozilla/5.0 (spa-api-recon spider)"
SKIP_EXT = re.compile(r'\.(css|png|jpe?g|gif|svg|ico|woff2?|ttf|pdf|zip|mp4|webp|js)(\?|$)', re.I)
API_HINT = re.compile(r'/(?:api|rest|service|ajax|action|do|rpc|graphql|v\d|admin|backend)\b', re.I)

def origin_of(raw):
    try:
        u = urllib.parse.urlsplit(raw)
        if u.scheme.lower() not in ("http", "https") or not u.hostname or u.username is not None or u.password is not None:
            return None
        return (u.scheme.lower(), u.hostname.lower(), u.port if u.port is not None else (443 if u.scheme.lower() == "https" else 80))
    except ValueError:
        return None

class OriginRedirectHandler(urllib.request.HTTPRedirectHandler):
    def __init__(self, approved_origin):
        self.approved_origin = approved_origin

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if origin_of(newurl) != self.approved_origin:
            raise urllib.error.HTTPError(newurl, code, "Redirect outside approved origin", headers, fp)
        return super().redirect_request(req, fp, code, msg, headers, newurl)

def fetch(url, cookie, approved_origin, budget=None, tls_policy=None):
    if origin_of(url) != approved_origin:
        return "", ""
    h = {"User-Agent": UA}
    if cookie: h["Cookie"] = cookie
    try:
        budget = budget or ResponseBudget()
        budget.begin()
        opener = urllib.request.build_opener((tls_policy or DEFAULT_TLS).handler(), OriginRedirectHandler(approved_origin))
        with opener.open(urllib.request.Request(url, headers=h), timeout=20) as r:
            ct = r.headers.get("Content-Type", "")
            if "html" not in ct and "xml" not in ct: return "", ct
            return budget.read(r).decode("utf-8", "ignore"), ct
    except ResponseLimitError as e:
        print(f"[!] response omitted: {e}", file=sys.stderr)
        return "", ""
    except Exception as e:
        report_tls_failure(e)
        if isinstance(e, urllib.error.HTTPError): e.close()
        return "", ""

class Page(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links, self.scripts, self.inline = [], [], []
        self.forms, self._cur = [], None
        self._in_script = False
    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "a" and a.get("href"): self.links.append(a["href"])
        elif tag == "script":
            self._in_script = True
            if a.get("src"): self.scripts.append(a["src"])
        elif tag == "form":
            self._cur = {"action": a.get("action", ""), "method": (a.get("method") or "GET").upper(), "params": []}
        elif tag in ("input", "select", "textarea", "button") and self._cur is not None:
            n = a.get("name")
            if n: self._cur["params"].append(n)
        # ajax-ish handlers
        for v in a.values():
            for m in re.findall(r'''["'](/[^"']{2,120})["']''', v or ""):
                if API_HINT.search(m): self.inline.append(m)
    def handle_endtag(self, tag):
        if tag == "script": self._in_script = False
        elif tag == "form" and self._cur is not None:
            self.forms.append(self._cur); self._cur = None
    def handle_data(self, data):
        if self._in_script and data:
            for m in re.findall(r'''["'`](/[A-Za-z0-9_\-./{}$:?=&]{2,120})["'`]''', data):
                if API_HINT.search(m): self.inline.append(m)

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base"); ap.add_argument("outdir")
    ap.add_argument("--cookie", default=""); ap.add_argument("--max", type=int, default=200)
    ap.add_argument("--depth", type=int, default=4); ap.add_argument("--delay", type=float, default=0.1)
    ap.add_argument("--include", default=""); ap.add_argument("--exclude", default="logout|signout|delete|remove|destroy")
    add_budget_arguments(ap)
    add_tls_arguments(ap)
    args = ap.parse_args()
    budget = budget_from_args(args)
    base = args.base if args.base.startswith("http") else "https://" + args.base
    approved_origin = origin_of(base)
    if approved_origin is None:
        ap.error("base must be an HTTP(S) URL without embedded credentials")
    tls_policy = tls_from_args(ap, args, base)
    os.makedirs(args.outdir, exist_ok=True)
    origin = "{0.scheme}://{0.netloc}".format(urllib.parse.urlparse(base))
    inc = re.compile(args.include) if args.include else None
    exc = re.compile(args.exclude, re.I) if args.exclude else None

    seen, forms, inline, links = set(), {}, set(), set()
    q = deque([(base, 0)]); seen.add(base.split("#")[0])
    n = 0
    while q and n < args.max:
        url, d = q.popleft(); n += 1
        html, ct = fetch(url, args.cookie, approved_origin, budget, tls_policy)
        if args.delay: time.sleep(args.delay)
        if not html: continue
        p = Page()
        try: p.feed(html)
        except Exception: pass
        links.add(url.replace(origin, "") or "/")
        for f in p.forms:
            act = urllib.parse.urljoin(url, f["action"] or url)
            key = f["method"] + " " + act.replace(origin, "")
            forms.setdefault(key, set()).update(f["params"])
        for s in p.inline: inline.add(s)
        if d < args.depth:
            for href in p.links:
                if href.startswith(("mailto:", "tel:", "javascript:", "#")): continue
                nxt = urllib.parse.urljoin(url, href).split("#")[0]
                if origin_of(nxt) != approved_origin: continue
                if SKIP_EXT.search(nxt): continue
                if exc and exc.search(nxt): continue
                if inc and not inc.search(nxt): continue
                if nxt not in seen:
                    seen.add(nxt); q.append((nxt, d + 1))

    with open(os.path.join(args.outdir, "forms.txt"), "w") as fh:
        for k in sorted(forms):
            ps = ", ".join(sorted(forms[k]))
            fh.write(f"{k}  [{ps}]\n")
    open(os.path.join(args.outdir, "links.txt"), "w").write("\n".join(sorted(links)))
    open(os.path.join(args.outdir, "api_inline.txt"), "w").write("\n".join(sorted(inline)))

    print(f"[+] crawled {n} pages (cap {args.max}, depth {args.depth})")
    print(f"[+] forms (endpoints): {len(forms)}  -> forms.txt")
    print(f"[+] inline ajax urls:  {len(inline)} -> api_inline.txt")
    print(f"[+] pages reached:     {len(links)} -> links.txt")
    if not args.cookie:
        print("[i] no --cookie: only public pages crawled. Pass an authed session cookie for the full surface.")

if __name__ == "__main__":
    main()
