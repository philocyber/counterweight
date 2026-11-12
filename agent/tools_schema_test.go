package agent

import (
	"encoding/json"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

// A schema built by the helper makes the SDK reject a call that leaves out a
// required field before the tool's handler is reached.
func TestSchemaHelperRequiredIsEnforced(t *testing.T) {
	schema := obj(map[string]any{"target": str("fixture")}, "target")
	if err := actool.ValidateInput(schema, json.RawMessage(`{}`)); err == nil {
		t.Fatal("a call without its required field passed validation")
	}
	if err := actool.ValidateInput(schema, json.RawMessage(`{"target":"fixture"}`)); err != nil {
		t.Fatalf("a complete call was rejected: %v", err)
	}
}
