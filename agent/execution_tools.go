package agent

import (
	"context"
	"encoding/json"
	"errors"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/execution"
)

// FileRoots is supplied by the server from persisted task/conversation identity.
// A nil hook leaves standalone SDK uses and platform assistant sessions unchanged.
var FileRoots func(context.Context, string) (execution.Roots, bool, error)
var SkillReadRoot string
var FileAccessFailed func(context.Context, error)

// FileAccess binds file admission to the runner's owning server.
type FileAccess struct {
	workingDir string
	roots      func(context.Context, string) (execution.Roots, bool, error)
	failed     func(context.Context, error)
}

type fileAccessKey struct{}

func (a *FileAccess) SetFileAccess(roots func(context.Context, string) (execution.Roots, bool, error), failed func(context.Context, error)) {
	a.roots, a.failed = roots, failed
}

func WithFileAccess(ctx context.Context, roots func(context.Context, string) (execution.Roots, bool, error), failed func(context.Context, error)) context.Context {
	return context.WithValue(ctx, fileAccessKey{}, &FileAccess{roots: roots, failed: failed})
}

func (a FileAccess) context(ctx context.Context) context.Context {
	if a.roots != nil {
		return WithFileAccess(ctx, a.roots, a.failed)
	}
	return ctx
}

func confineFileTools(ctx context.Context, key string, tools []actool.CoreTool) ([]actool.CoreTool, func()) {
	noop := func() {}
	rootsFor, failed := FileRoots, FileAccessFailed
	if a, ok := ctx.Value(fileAccessKey{}).(*FileAccess); ok {
		rootsFor, failed = a.roots, a.failed
	}
	if rootsFor == nil {
		return tools, noop
	}
	roots, on, err := rootsFor(ctx, key)
	if !on && err == nil {
		return tools, noop
	}
	if a, ok := ctx.Value(fileAccessKey{}).(*FileAccess); ok {
		a.workingDir = roots.WorkingDir
	}
	var files *execution.Files
	if err == nil {
		if SkillReadRoot != "" {
			roots.Read = append(roots.Read, SkillReadRoot)
		}
		files, err = execution.Open(roots)
	}
	if err != nil {
		if failed != nil {
			failed(ctx, err)
		}
		return []actool.CoreTool{actool.Build(actool.Spec{Name: "execution_admission_failed", Schema: map[string]any{"type": "object"}, Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
			return actool.Result{}, err
		}})}, noop
	}
	out := make([]actool.CoreTool, len(tools))
	for i, t := range tools {
		out[i] = files.Tool(t)
		if roots.Mode == execution.Container && t.Name() == "Bash" {
			out[i] = &containerBash{CoreTool: t, roots: roots, failed: failed}
		}
	}
	return out, files.Close
}
func fileAdmissionError(tools []actool.CoreTool) error {
	for _, t := range tools {
		if t.Name() == "execution_admission_failed" {
			_, err := t.Call(context.Background(), nil, nil)
			if err == nil {
				err = errors.New("File confinement admission failed")
			}
			return err
		}
	}
	return nil
}

func fileWorkingDirectory(ctx context.Context, fallback string) string {
	if a, ok := ctx.Value(fileAccessKey{}).(*FileAccess); ok && a.workingDir != "" {
		return a.workingDir
	}
	return fallback
}
