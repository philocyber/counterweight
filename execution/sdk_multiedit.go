// Adapted from github.com/Autumn-27/norma v0.4.3 tool/multiedit.go.
// File operations use os.Root; search always uses the SDK Go fallback.
package execution

import (
	"context"
	"encoding/json"
	"fmt"
	actool "github.com/Autumn-27/norma/tool"
	"strings"
)

func (c *confinedFS) runMultiEdit(_ context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Edits    []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return actool.Result{}, err
	}
	if len(in.Edits) == 0 {
		return actool.Errorf("Error: no edits provided"), nil
	}
	path := resolvePath(tc, in.FilePath)
	data, tooBig, err := c.readFileMax(path, maxEditBytes)
	if err != nil {
		return actool.Errorf(fmt.Sprintf("Error reading %s: %v", in.FilePath, err)), nil
	}
	if tooBig {
		return actool.Errorf(fmt.Sprintf("Error: %s is larger than %d bytes and cannot be edited with this tool", in.FilePath, maxEditBytes)), nil
	}
	content := string(data)
	total := 0
	for i, e := range in.Edits {
		if e.OldString == e.NewString {
			return actool.Errorf(fmt.Sprintf("Error in edit %d: old_string and new_string are identical", i+1)), nil
		}
		count := strings.Count(content, e.OldString)
		if count == 0 {
			return actool.Errorf(fmt.Sprintf("Error in edit %d: old_string not found", i+1)), nil
		}
		if count > 1 && !e.ReplaceAll {
			return actool.Errorf(fmt.Sprintf("Error in edit %d: old_string is not unique (%d matches); add context or set replace_all", i+1, count)), nil
		}
		if e.ReplaceAll {
			content = strings.ReplaceAll(content, e.OldString, e.NewString)
		} else {
			content = strings.Replace(content, e.OldString, e.NewString, 1)
		}
		total += count
	}
	if err := c.writeFile(path, []byte(content), 0o644); err != nil {
		return actool.Errorf(fmt.Sprintf("Error writing %s: %v", in.FilePath, err)), nil
	}
	return actool.Text(fmt.Sprintf("Applied %d edits to %s (%d replacement(s))", len(in.Edits), in.FilePath, total)), nil
}
