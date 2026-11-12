package agent

import "encoding/json"

func cloneToolSchema(schema map[string]any) map[string]any {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return out
}
