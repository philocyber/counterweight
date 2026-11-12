#!/bin/sh
# Starts a Counterweight review of the workflow's pull request, waits for it,
# writes the job summary, saves the SARIF file and optionally comments on the
# pull request. Needs only sh, curl and jq.
#
# Everything from the event payload is data: it is read with jq, quoted, and sent
# as JSON built with `jq --arg`. Nothing is evaluated. The API token is never
# printed and never placed on a command line: curl reads it from a private file.
#
# Exit codes: 0 ok, 1 a finding at or above the threshold is inside the change,
# 2 the review could not be run or completed.
#
# Environment (set by action.yml):
#   CW_URL, CW_TOKEN, CW_DEPTH, CW_FAIL_ON, CW_COMMENT, CW_TIMEOUT, CW_ALLOW_INSECURE
#   GITHUB_EVENT_PATH, GITHUB_REPOSITORY, GITHUB_API_URL, GITHUB_STEP_SUMMARY,
#   GITHUB_OUTPUT, RUNNER_TEMP, GH_TOKEN (only to comment)

set -eu
umask 077

say() { printf 'Counterweight review: %s\n' "$*"; }
warn() { printf '::warning::Counterweight review: %s\n' "$(printf '%s' "$*" | tr -d '\r\n')"; }
fail() {
	printf '::error::Counterweight review: %s\n' "$(printf '%s' "$*" | tr -d '\r\n')" >&2
	exit 2
}

need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required on the runner"; }
need curl
need jq

: "${CW_URL:=}" "${CW_DEPTH:=balanced}" "${CW_FAIL_ON:=none}" "${CW_COMMENT:=false}" "${CW_TIMEOUT:=1800}" "${CW_ALLOW_INSECURE:=false}"

# Instance URL: https only, unless the operator opted in.
CW_URL=${CW_URL%/}
case "$CW_URL" in
https://?*) PROTO='=https' ;;
http://?*)
	[ "$CW_ALLOW_INSECURE" = true ] || fail "the instance URL is not https. Use an https address, or set allow-insecure to true for a trusted network."
	PROTO='=https,http'
	;;
*) fail "the instance URL must start with https://" ;;
esac
case "$CW_URL" in
*[[:space:]]*) fail "the instance URL must not contain whitespace" ;;
esac
[ -n "${CW_TOKEN:-}" ] || fail "the API token is empty. Secrets are not available to workflows from forks; store the token as a repository secret."

case "$CW_DEPTH" in light | balanced | comprehensive) ;; *) fail "depth must be light, balanced or comprehensive" ;; esac
case "$CW_FAIL_ON" in none | low | medium | high | critical) ;; *) fail "fail-on must be none, low, medium, high or critical" ;; esac
case "$CW_TIMEOUT" in '' | *[!0-9]*) fail "timeout must be a number of seconds" ;; esac
[ "$CW_TIMEOUT" -ge 60 ] || fail "timeout must be at least 60 seconds"

# The pull request, from the event payload.
[ -r "${GITHUB_EVENT_PATH:-}" ] || fail "no event payload; run this action on a pull_request event"
PR=$(jq -r '.pull_request.number // empty' "$GITHUB_EVENT_PATH")
BASE_REF=$(jq -r '.pull_request.base.ref // empty' "$GITHUB_EVENT_PATH")
HEAD_SHA=$(jq -r '.pull_request.head.sha // empty' "$GITHUB_EVENT_PATH")
case "$PR" in '' | *[!0-9]*) fail "this workflow run is not for a pull request; use the pull_request event" ;; esac
[ -n "${GITHUB_REPOSITORY:-}" ] || fail "GITHUB_REPOSITORY is not set"

WORK=$(mktemp -d "${RUNNER_TEMP:-/tmp}/counterweight.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

# The token goes to curl through a private header file, never on a command line.
HDR="$WORK/auth"
printf 'Authorization: Bearer %s\n' "$CW_TOKEN" >"$HDR"

# api METHOD PATH OUTFILE [DATAFILE] [MAXTIME]: prints the HTTP status ("000" when
# the request did not complete). Redirects are not followed, so the token cannot
# be sent to another host.
api() {
	_data=
	[ -n "${4:-}" ] && _data="$4"
	if [ -n "$_data" ]; then
		curl -sS --proto "$PROTO" --proto-redir =https --max-time "${5:-60}" -o "$3" -w '%{http_code}' \
			-X "$1" -H "@$HDR" -H 'Accept: application/json' -H 'Content-Type: application/json' \
			--data-binary "@$_data" "$CW_URL$2" 2>"$WORK/curl.err" || true
	else
		curl -sS --proto "$PROTO" --proto-redir =https --max-time "${5:-60}" -o "$3" -w '%{http_code}' \
			-X "$1" -H "@$HDR" -H 'Accept: application/json' "$CW_URL$2" 2>"$WORK/curl.err" || true
	fi
}

# server_error FILE: the server's error text, one short line.
server_error() {
	jq -r '.error // empty' "$1" 2>/dev/null | tr -d '\r\n' | cut -c1-300
}

# --- start ------------------------------------------------------------------

BODY="$WORK/start.json"
if printf '%s' "$HEAD_SHA" | grep -Eq '^[0-9a-f]{40}$'; then
	jq -n --arg repo "$GITHUB_REPOSITORY" --argjson n "$PR" --arg base "$BASE_REF" --arg head "$HEAD_SHA" --arg depth "$CW_DEPTH" \
		'{repository:$repo, pull_number:$n, base:$base, head:$head, depth:$depth} | with_entries(select(.value != ""))' >"$BODY"
else
	jq -n --arg repo "$GITHUB_REPOSITORY" --argjson n "$PR" --arg base "$BASE_REF" --arg depth "$CW_DEPTH" \
		'{repository:$repo, pull_number:$n, base:$base, depth:$depth} | with_entries(select(.value != ""))' >"$BODY"
fi

say "starting the review of $GITHUB_REPOSITORY#$PR"
# Cloning a large repository can take minutes before the server answers.
CODE=$(api POST /api/reviews "$WORK/start.out" "$BODY" 700)
case "$CODE" in
200 | 201) ;;
000) fail "could not reach the instance: $(tr -d '\r\n' <"$WORK/curl.err" | cut -c1-200)" ;;
401) fail "the instance rejected the API token (HTTP 401): it is wrong, revoked or expired" ;;
403) fail "the API token may not start reviews (HTTP 403): $(server_error "$WORK/start.out"). It needs the reviews:write scope." ;;
*) fail "the instance could not start the review (HTTP $CODE): $(server_error "$WORK/start.out")" ;;
esac
ID=$(jq -r '.id // empty' "$WORK/start.out")
case "$ID" in '' | *[!0-9]*) fail "the instance returned an unexpected reply to the start request" ;; esac
if [ "$(jq -r '.existing // false' "$WORK/start.out")" = true ]; then
	say "the instance already has a review of this commit; using it"
fi

# --- wait -------------------------------------------------------------------

START=$(date +%s)
DELAY=5
ERRORS=0
while :; do
	CODE=$(api GET "/api/reviews/$ID" "$WORK/status.json")
	case "$CODE" in
	200)
		ERRORS=0
		STATUS=$(jq -r '.status // empty' "$WORK/status.json")
		case "$STATUS" in
		finished) break ;;
		failed) fail "the review failed on the instance: $(jq -r '.url // empty' "$WORK/status.json")" ;;
		waiting) say "waiting: $(jq -r '.waiting_reason // "paused"' "$WORK/status.json" | tr -d '\r\n' | cut -c1-60)" ;;
		queued | running) say "$STATUS" ;;
		*) fail "unexpected review status from the instance" ;;
		esac
		;;
	000 | 5??)
		ERRORS=$((ERRORS + 1))
		[ "$ERRORS" -lt 4 ] || fail "the instance stopped answering (HTTP $CODE)"
		;;
	401 | 403) fail "the API token may not read reviews (HTTP $CODE). It needs the reviews:read scope." ;;
	*) fail "could not read the review (HTTP $CODE): $(server_error "$WORK/status.json")" ;;
	esac
	NOW=$(date +%s)
	ELAPSED=$((NOW - START))
	if [ "$ELAPSED" -ge "$CW_TIMEOUT" ]; then
		fail "timed out after $CW_TIMEOUT seconds. The review continues on the instance: $(jq -r '.url // empty' "$WORK/status.json" 2>/dev/null)"
	fi
	LEFT=$((CW_TIMEOUT - ELAPSED))
	[ "$DELAY" -le "$LEFT" ] || DELAY=$LEFT
	sleep "$DELAY"
	DELAY=$((DELAY * 2))
	[ "$DELAY" -le 30 ] || DELAY=30
done

# --- results ----------------------------------------------------------------

CODE=$(api GET "/api/reviews/$ID/summary" "$WORK/summary.md")
[ "$CODE" = 200 ] || fail "could not read the summary (HTTP $CODE)"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	cat "$WORK/summary.md" >>"$GITHUB_STEP_SUMMARY"
fi

CODE=$(api GET "/api/reviews/$ID/sarif?only_change=true" "$WORK/review.sarif")
[ "$CODE" = 200 ] || fail "could not read the SARIF results (HTTP $CODE)"
SARIF="${RUNNER_TEMP:-/tmp}/counterweight-$ID.sarif"
cp "$WORK/review.sarif" "$SARIF"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
	printf 'sarif=%s\nreview-id=%s\n' "$SARIF" "$ID" >>"$GITHUB_OUTPUT"
fi

# One comment, updated in place. A failure here is a warning: the results are
# already in the job summary.
if [ "$CW_COMMENT" = true ]; then
	MARKER='<!-- counterweight-review -->'
	GH_API=${GITHUB_API_URL:-https://api.github.com}
	if [ -z "${GH_TOKEN:-}" ]; then
		warn "no GitHub token for the comment; the workflow needs permissions: pull-requests: write"
	else
		GHH="$WORK/gh-auth"
		printf 'Authorization: Bearer %s\n' "$GH_TOKEN" >"$GHH"
		gh() { # METHOD URL OUTFILE [DATAFILE]
			if [ -n "${4:-}" ]; then
				curl -sS --proto =https,http --proto-redir =https --max-time 60 -o "$3" -w '%{http_code}' -X "$1" -H "@$GHH" \
					-H 'Accept: application/vnd.github+json' -H 'Content-Type: application/json' --data-binary "@$4" "$2" 2>/dev/null || true
			else
				curl -sS --proto =https,http --proto-redir =https --max-time 60 -o "$3" -w '%{http_code}' -X "$1" -H "@$GHH" \
					-H 'Accept: application/vnd.github+json' "$2" 2>/dev/null || true
			fi
		}
		{ printf '%s\n' "$MARKER"; cat "$WORK/summary.md"; } >"$WORK/comment.md"
		jq -Rs '{body: .}' "$WORK/comment.md" >"$WORK/comment.json"
		COMMENT_ID=
		PAGE=1
		while [ "$PAGE" -le 5 ] && [ -z "$COMMENT_ID" ]; do
			C=$(gh GET "$GH_API/repos/$GITHUB_REPOSITORY/issues/$PR/comments?per_page=100&page=$PAGE" "$WORK/comments.json")
			[ "$C" = 200 ] || break
			COMMENT_ID=$(jq -r --arg m "$MARKER" '[.[] | select(.body | startswith($m))][0].id // empty' "$WORK/comments.json")
			[ "$(jq 'length' "$WORK/comments.json")" -eq 100 ] || break
			PAGE=$((PAGE + 1))
		done
		case "$COMMENT_ID" in *[!0-9]*) COMMENT_ID= ;; esac
		if [ -n "$COMMENT_ID" ]; then
			C=$(gh PATCH "$GH_API/repos/$GITHUB_REPOSITORY/issues/comments/$COMMENT_ID" "$WORK/posted.json" "$WORK/comment.json")
		else
			C=$(gh POST "$GH_API/repos/$GITHUB_REPOSITORY/issues/$PR/comments" "$WORK/posted.json" "$WORK/comment.json")
		fi
		case "$C" in 200 | 201) say "pull request comment updated" ;; *) warn "could not write the pull request comment (HTTP $C); check the workflow permissions" ;; esac
	fi
fi

# --- gate -------------------------------------------------------------------

if [ "$CW_FAIL_ON" != none ]; then
	HITS=$(jq -r --arg min "$CW_FAIL_ON" '
		def rank: {"critical":4,"high":3,"medium":2,"low":1};
		.counts.in_change as $c
		| [ ("critical","high","medium","low") as $s | select(rank[$s] >= rank[$min]) | ($c[$s] // 0) ] | add // 0' "$WORK/status.json")
	case "$HITS" in '' | *[!0-9]*) fail "could not read the finding counts" ;; esac
	if [ "$HITS" -gt 0 ]; then
		say "$HITS finding(s) at or above $CW_FAIL_ON are inside the change"
		exit 1
	fi
fi
say "done"
