package agent

import (
	"context"
	"testing"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

type resultHookFixture struct {
	name  string
	input string
}

func (*resultHookFixture) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	return false, "", nil
}
func (h *resultHookFixture) PostToolUse(_ context.Context, name string, input, _ []byte, _ bool) {
	h.name, h.input = name, string(input)
}
func (*resultHookFixture) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return false, nil, ""
}

// The result of a tool run through the SDK wrapper is reported under the tool
// and params that were reviewed, as the result of a direct call is. A hook
// that follows what a tool did therefore sees both the same way.
func TestWrappedToolResultIsReportedUnderItsOwnName(t *testing.T) {
	inner := &resultHookFixture{}
	hooks := &operationHooks{inner: inner}
	hooks.PostToolUse(context.Background(), actool.ExecuteExtraToolName, []byte(`{"tool_name":"shell_send","params":{"session_id":"sh_1","text":"r"}}`), nil, false)
	if inner.name != "shell_send" || inner.input != `{"session_id":"sh_1","text":"r"}` {
		t.Fatalf("wrapped result reported as %s %s", inner.name, inner.input)
	}
	hooks.PostToolUse(context.Background(), "shell_send", []byte(`{"session_id":"sh_1","text":"m"}`), nil, false)
	if inner.name != "shell_send" || inner.input != `{"session_id":"sh_1","text":"m"}` {
		t.Fatalf("direct result reported as %s %s", inner.name, inner.input)
	}
	// A wrapper call the SDK rejects without running a tool keeps its own name.
	hooks.PostToolUse(context.Background(), actool.ExecuteExtraToolName, []byte(`{"params":{}}`), nil, true)
	if inner.name != actool.ExecuteExtraToolName {
		t.Fatalf("rejected wrapper call reported as %s", inner.name)
	}
}
