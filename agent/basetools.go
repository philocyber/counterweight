package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// Base SDK tools per chat agent. Code-review agents get a restricted set so
// "never run repository code" and "change only the fix worktree" are enforced by
// the tool set rather than by the prompt alone.

// readOnlyBaseAgents receive only the SDK's read-only file tools: no Bash, Write,
// Edit or MultiEdit. The standalone review chat agent reads untrusted source too.
var readOnlyBaseAgents = map[string]bool{CodeReviewChatAgentKey: true, db.CodeReviewPlannerAgentKey: true, db.CodeReviewWorkerAgentKey: true, db.CodeVerifierAgentKey: true, db.PatchReviewerAgentKey: true}

// rootedWriteAgents receive the read-only tools plus Write/Edit/MultiEdit limited
// to the directory WriteRoot resolves for the running conversation. No Bash.
var rootedWriteAgents = map[string]bool{db.CodeFixerAgentKey: true}

var readOnlyBaseTools = map[string]bool{"Read": true, "LS": true, "Glob": true, "Grep": true}

var writeBaseTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true}

// WriteRoot resolves the only directory a rooted-write agent may change, from the
// running conversation in ctx. Set by the server; nil or "" denies every write.
var WriteRoot func(ctx context.Context) string

// baseToolsFor returns the SDK base tools a chat agent starts from.
func baseToolsFor(agentKey string) []actool.CoreTool {
	all := actool.DefaultTools()
	if !readOnlyBaseAgents[agentKey] && !rootedWriteAgents[agentKey] {
		return all
	}
	var keep []actool.CoreTool
	for _, t := range all {
		switch {
		case readOnlyBaseTools[t.Name()]:
			keep = append(keep, t)
		case rootedWriteAgents[agentKey] && writeBaseTools[t.Name()]:
			keep = append(keep, rootedWrite{t})
		}
	}
	return keep
}

// rootedWrite refuses a write whose target, after resolving symbolic links, is
// not inside the conversation's write root.
type rootedWrite struct{ actool.CoreTool }

func (w rootedWrite) Call(ctx context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
	}
	_ = json.Unmarshal(input, &in)
	root := ""
	if WriteRoot != nil {
		root = WriteRoot(ctx)
	}
	if !insideRoot(root, in.FilePath) {
		return actool.Errorf(locale.Text(locale.FromContext(ctx), "Writes are allowed only to absolute paths inside the fix worktree %s", root)), nil
	}
	return w.CoreTool.Call(ctx, input, tc)
}

// insideRoot reports whether path is an absolute path within root once symbolic
// links in its existing ancestors are resolved, so a link committed to the
// repository cannot redirect a write outside the worktree.
func insideRoot(root, path string) bool {
	if root == "" || path == "" || !filepath.IsAbs(path) {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	// Resolve the deepest existing ancestor; the rest of the path is created fresh.
	existing, rest := filepath.Clean(path), ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return false
	}
	full := filepath.Join(resolved, rest)
	rel, err := filepath.Rel(realRoot, full)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	// The worktree's .git file points at the review clone; never let it change.
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	return !strings.EqualFold(first, ".git")
}
