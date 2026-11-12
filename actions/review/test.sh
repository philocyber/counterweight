#!/bin/sh
# Runs review.sh against a local fake Counterweight server (python3 http.server).
# Needs sh, curl, jq and python3; prints SKIP and exits 0 when one is missing.
# Usage: sh actions/review/test.sh
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
for c in curl jq python3; do
	command -v "$c" >/dev/null 2>&1 || { echo "SKIP: $c not installed"; exit 0; }
done
T=$(mktemp -d)
trap 'kill "$SRV" 2>/dev/null; rm -rf "$T"' EXIT

cat >"$T/fake.py" <<'PY'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
mode = sys.argv[2]
polls = {"n": 0}
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def send(self, code, body, ctype="application/json"):
        raw = body.encode(); self.send_response(code)
        self.send_header("Content-Type", ctype); self.send_header("Content-Length", str(len(raw))); self.end_headers(); self.wfile.write(raw)
    def auth(self):
        if self.headers.get("Authorization") != "Bearer cwt_test": self.send(401, '{"error":"bad token"}'); return False
        return True
    def do_POST(self):
        if not self.auth(): return
        n = int(self.headers.get("Content-Length", "0")); body = json.loads(self.rfile.read(n))
        open(sys.argv[3], "w").write(json.dumps(body))
        self.send(201, '{"id":"5","task_id":"5","status":"queued","url":"http://x/t"}')
    def do_GET(self):
        if not self.auth(): return
        p = self.path.split("?")[0]
        if p == "/api/reviews/5":
            polls["n"] += 1
            st = "running" if polls["n"] < 2 else "finished"
            high = 1 if mode == "high" else 0
            self.send(200, json.dumps({"id": "5", "status": st, "url": "http://x/t",
                "counts": {"in_change": {"critical": 0, "high": high, "medium": 0, "low": 0}, "outside_change": {}}}))
        elif p.endswith("/summary"): self.send(200, "## Summary\n", "text/markdown")
        elif p.endswith("/sarif"): self.send(200, '{"version":"2.1.0","runs":[]}', "application/sarif+json")
        else: self.send(404, "{}")
HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY

run() { # MODE FAILON -> exit code
	PORT=$((20000 + $$ % 20000))
	python3 "$T/fake.py" "$PORT" "$1" "$T/start.json" & SRV=$!
	sleep 1
	cat >"$T/event.json" <<'EOF'
{"pull_request":{"number":7,"base":{"ref":"main"},"head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
EOF
	: >"$T/summary.md"; : >"$T/output"
	CW_URL="http://127.0.0.1:$PORT" CW_ALLOW_INSECURE=true CW_TOKEN="${3:-cwt_test}" CW_FAIL_ON="$2" CW_TIMEOUT=120 \
		GITHUB_EVENT_PATH="$T/event.json" GITHUB_REPOSITORY=acme/example GITHUB_STEP_SUMMARY="$T/summary.md" \
		GITHUB_OUTPUT="$T/output" RUNNER_TEMP="$T" sh "$HERE/review.sh" >"$T/log" 2>&1
	rc=$?
	kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null
	return $rc
}
ok=0
check() { if [ "$2" = "$3" ]; then echo "PASS $1"; else echo "FAIL $1: got $3 want $2"; ok=1; fi; }

run clean none; check "finished review passes" 0 $?
grep -q '"pull_number": *7' "$T/start.json" && grep -q '"repository": *"acme/example"' "$T/start.json"; check "start request built from the event" 0 $?
grep -q '^## Summary' "$T/summary.md"; check "summary written to the job summary" 0 $?
grep -q '^sarif=' "$T/output"; check "sarif path exported" 0 $?
run high high; check "finding at the threshold fails" 1 $?
run high critical; check "finding below the threshold passes" 0 $?
run clean none cwt_wrong; check "bad token fails closed" 2 $?
grep -q cwt_wrong "$T/log"; check "token not printed" 1 $?
CW_URL=http://example.invalid CW_TOKEN=x GITHUB_EVENT_PATH="$T/event.json" GITHUB_REPOSITORY=a/b sh "$HERE/review.sh" >/dev/null 2>&1; check "http refused without allow-insecure" 2 $?
exit $ok
