package intercept

import (
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

func TestApprovalResumeAllowsStandingGrantAndExactReplay(t *testing.T) {
	input := []byte(`{"url":"https://example.test"}`)
	message := ApprovalDeferredMessage(locale.En)
	if !strings.HasPrefix(message, ApprovalDeferredMarker) {
		t.Fatal("deferred result lost its marker")
	}
	lower := strings.ToLower(message)
	for _, banned := range []string{"prohibited", "do not retry"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("deferred result tells the worker to stop: %s", banned)
		}
	}

	all := ApprovalResume{Grants: []db.TaskToolGrant{{Scope: "all"}}}
	if !all.Allows("WebFetch", input) {
		t.Fatal("approve-every-tool grant did not cover the paused call")
	}
	exact := ApprovalResume{Replays: []ExactReplay{{Tool: "WebFetch", Input: input}}}
	if !exact.Allows("WebFetch", []byte(`{"url":"https://example.test"}`)) {
		t.Fatal("exact approval did not cover the same arguments")
	}
	if exact.Allows("WebFetch", []byte(`{"url":"https://other.test"}`)) {
		t.Fatal("exact approval covered different arguments")
	}
	if (ApprovalResume{}).Allows("WebFetch", input) {
		t.Fatal("unapproved call was allowed")
	}
}
