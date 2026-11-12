// Adapted from github.com/Autumn-27/norma v0.4.3 tool/ls.go.
// File operations use os.Root; search always uses the SDK Go fallback.
package execution

import (
	"context"
	"encoding/json"
	"fmt"
	actool "github.com/Autumn-27/norma/tool"
	"sort"
	"strings"
)

func (c *confinedFS) runLS(_ context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return actool.Result{}, err
	}
	dir := tc.WorkingDir
	if in.Path != "" {
		dir = resolvePath(tc, in.Path)
	}
	if dir == "" {
		dir = "."
	}
	entries, err := c.readDir(dir)
	if err != nil {
		return actool.Errorf(fmt.Sprintf("Error listing %s: %v", in.Path, err)), nil
	}
	if len(entries) == 0 {
		return actool.Text("(empty directory)"), nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir() // directories first
		}
		return entries[i].Name() < entries[j].Name()
	})
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		fmt.Fprintf(&b, "%s\t%d bytes\n", e.Name(), size)
	}
	return actool.Text(capture(tc, b.String())), nil
}
