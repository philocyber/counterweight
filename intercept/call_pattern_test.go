package intercept

import (
	"github.com/Philocyber/counterweight/db"
	"testing"
)

func TestCallPatternMatches(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, tool, input string
		want                       bool
	}{
		{"any parameters", `{"tool":"Bash"}`, "Bash", `{"command":"pwd"}`, true},
		{"exact tool", `{"tool":"Bash"}`, "OtherBash", `{}`, false},
		{"key order", `{"tool":"WebFetch","arguments":{"url":"https://example.gov","method":"GET"}}`, "WebFetch", `{"method":"GET","url":"https://example.gov"}`, true},
		{"extra argument", `{"tool":"Bash","arguments":{"command":"pwd"}}`, "Bash", `{"command":"pwd","cwd":"/tmp"}`, false},
		{"compound command", `{"tool":"Bash","arguments":{"command":"pwd"}}`, "Bash", `{"command":"pwd; rm file"}`, false},
		{"different tool", `{"tool":"Bash","arguments":{}}`, "Write", `{}`, false},
		{"invalid pattern", `{"tool":"Bash","arguments":null}`, "Bash", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CallPatternMatches(tc.pattern, tc.tool, []byte(tc.input)); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestArgumentGrants(t *testing.T) {
	grants := []db.TaskToolGrant{{Scope: "input", ToolName: "Write", Command: `{"file_path":"/tmp/report","content":"test"}`}}
	if !GrantAllows(grants, []string{"Write"}, []byte(`{"content":"test","file_path":"/tmp/report"}`)) {
		t.Fatal("equivalent object missed")
	}
	for _, input := range []string{`{"file_path":"/tmp/report","content":"changed"}`, `{"file_path":"/other","content":"test"}`, `null`, `{} trailing`} {
		if GrantAllows(grants, []string{"Write"}, []byte(input)) {
			t.Fatalf("unexpected grant: %s", input)
		}
	}
	if SameArguments([]byte(`{"id":9007199254740992}`), []byte(`{"id":9007199254740993}`)) {
		t.Fatal("large numbers rounded into an approval")
	}
}

func TestValidateCallPattern(t *testing.T) {
	for _, p := range []string{`{}`, `{"tool":"Bash","arguments":[]}`, `{"tool":"Bash","arguments":null}`, `{"tool":"Bash","typo":1}`, `{"tool":"Bash"} {}`} {
		if ValidateCallPattern(p) == nil {
			t.Fatalf("accepted invalid pattern: %s", p)
		}
	}
}
