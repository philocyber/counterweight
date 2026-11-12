// Package execution implements the portable task file boundary. It does not
// restrict processes, sockets, or tools supplied by external services.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/locale"
)

type Roots struct {
	WorkingDir   string
	Read         []string
	Write        []string
	Mode         string
	TaskID       string
	Workspace    string
	Backend      Backend
	CommandRead  []string
	CommandWrite []string
}
type openedRoot struct {
	path  string
	root  *os.Root
	write bool
}
type Files struct{ roots []openedRoot }

// Open pins directory handles for a run. No tool falls back to host file APIs.
func Open(roots Roots) (*Files, error) {
	f := &Files{}
	for _, group := range []struct {
		paths []string
		write bool
	}{{roots.Read, false}, {roots.Write, true}} {
		for _, p := range group.paths {
			if !filepath.IsAbs(p) {
				f.Close()
				return nil, errors.New("task file roots are unavailable")
			}
			r, err := os.OpenRoot(p)
			if err != nil {
				f.Close()
				return nil, errors.New("task file roots are unavailable")
			}
			f.roots = append(f.roots, openedRoot{filepath.Clean(p), r, group.write})
		}
	}
	if len(f.roots) == 0 {
		return nil, errors.New("task file roots are unavailable")
	}
	return f, nil
}
func (f *Files) Close() {
	for _, r := range f.roots {
		_ = r.root.Close()
	}
}

type confinedTool struct {
	actool.CoreTool
	files *Files
}

func (f *Files) Tool(t actool.CoreTool) actool.CoreTool {
	switch t.Name() {
	case "Read", "LS", "Glob", "Grep", "Write", "Edit", "MultiEdit":
		return &confinedTool{t, f}
	}
	return t
}
func (t *confinedTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if tc == nil {
		tc = &actool.ToolContext{}
	}
	c := &confinedFS{files: t.files, write: t.Name() == "Write" || t.Name() == "Edit" || t.Name() == "MultiEdit"}
	var p struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(in, &p); err != nil {
		return actool.Result{}, err
	}
	path := p.Path
	if t.Name() == "Read" || c.write {
		path = p.FilePath
	}
	path = resolvePath(tc, path)
	if _, _, err := c.resolve(path); err != nil {
		return denied(ctx), nil
	}
	var res actool.Result
	var err error
	switch t.Name() {
	case "Read":
		res, err = c.runRead(ctx, in, tc)
	case "LS":
		res, err = c.runLS(ctx, in, tc)
	case "Glob":
		res, err = c.runGlob(ctx, in, tc)
	case "Grep":
		res, err = c.runGrep(ctx, in, tc)
	case "Write":
		res, err = c.runWrite(ctx, in, tc)
	case "Edit":
		res, err = c.runEdit(ctx, in, tc)
	case "MultiEdit":
		res, err = c.runMultiEdit(ctx, in, tc)
	}
	// PathError may contain link targets or platform-specific host paths. Never
	// return those details, including for a missing/disallowed search root.
	if c.failed {
		return denied(ctx), nil
	}
	return res, err
}
func denied(ctx context.Context) actool.Result {
	return actool.Errorf(locale.Text(locale.FromContext(ctx), "Path is outside the task's files or is unavailable within its allowed roots."))
}

type confinedFS struct {
	files   *Files
	write   bool
	failed  bool
	skipped int   // files Grep left unsearched for being too large
	scanned int64 // bytes Grep has read in this call
}

func (c *confinedFS) resolve(p string) (*os.Root, string, error) {
	if !filepath.IsAbs(p) {
		return nil, "", fs.ErrPermission
	}
	for _, r := range c.files.roots {
		if c.write && !r.write {
			continue
		}
		rel, err := filepath.Rel(r.path, p)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		if c.write {
			for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
				if isGitName(part) {
					return nil, "", fs.ErrPermission
				}
			}
			// Preserve the fixer's .git protection for aliases inside the root.
			// Root still enforces the actual open if a link changes concurrently.
			var err error
			rel, err = writeName(r.root, rel)
			if err != nil {
				return nil, "", err
			}
		}
		return r.root, rel, nil
	}
	return nil, "", fs.ErrPermission
}

func writeName(root *os.Root, name string) (string, error) {
	for links := 0; links < 40; links++ {
		parts := strings.Split(filepath.ToSlash(name), "/")
		parent := "."
		changed := false
		for i, part := range parts {
			if isGitName(part) {
				return "", fs.ErrPermission
			}
			current := filepath.Join(parent, part)
			info, err := root.Lstat(current)
			if errors.Is(err, fs.ErrNotExist) {
				return name, nil
			}
			if err != nil {
				return "", fs.ErrPermission
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := root.Readlink(current)
				if err != nil || filepath.IsAbs(target) {
					return "", fs.ErrPermission
				}
				tail := filepath.FromSlash(strings.Join(parts[i+1:], "/"))
				name = filepath.Join(parent, target, tail)
				if !filepath.IsLocal(name) {
					return "", fs.ErrPermission
				}
				changed = true
				break
			}
			parent = current
		}
		if !changed {
			return name, nil
		}
	}
	return "", fs.ErrPermission
}
func (c *confinedFS) stat(p string) (fs.FileInfo, error) {
	r, n, e := c.resolve(p)
	if e == nil {
		var i fs.FileInfo
		i, e = r.Stat(n)
		if e == nil {
			return i, nil
		}
	}
	if errors.Is(e, fs.ErrNotExist) {
		return nil, fs.ErrNotExist
	}
	c.failed = true
	return nil, fs.ErrPermission
}

// Per-call limits that keep one file-tool call's memory and work bounded no
// matter how large the files under the roots are.
const (
	maxEditBytes    = 8 << 20   // largest file Edit and MultiEdit will load
	maxGrepFileSize = 2 << 20   // largest single file Grep will scan
	maxGrepBytes    = 256 << 20 // total bytes Grep reads in one call
	maxGrepEntries  = 100000    // files Grep visits in one call
	maxReadScan     = 256 << 20 // bytes Read will stream past to reach the offset
)

// open opens a regular file for reading through the pinned root.
func (c *confinedFS) open(p string) (*os.File, error) {
	r, n, e := c.resolve(p)
	if e == nil {
		// Opening a FIFO blocks until something writes to it, so a FIFO, socket
		// or device is refused before the open.
		var info fs.FileInfo
		if info, e = r.Stat(n); e == nil && info.Mode()&(fs.ModeNamedPipe|fs.ModeSocket|fs.ModeDevice) != 0 {
			e = fs.ErrInvalid
		}
	}
	if e == nil {
		var f *os.File
		f, e = r.Open(n)
		if e == nil {
			return f, nil
		}
	}
	c.failed = true
	return nil, fs.ErrPermission
}

// readFileMax reads a whole file but never holds more than max bytes. A file
// above the limit is reported through tooBig, not as an error, so it does not
// turn the whole call into a denial.
func (c *confinedFS) readFileMax(p string, max int64) (b []byte, tooBig bool, err error) {
	f, err := c.open(p)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil {
		c.failed = true
		return nil, false, fs.ErrPermission
	}
	if int64(len(b)) > max {
		return nil, true, nil
	}
	return b, false, nil
}
func (c *confinedFS) writeFile(p string, b []byte, perm fs.FileMode) error {
	r, n, e := c.resolve(p)
	if e == nil {
		e = writeGuarded(r, n, b, perm)
	}
	if e != nil {
		c.failed = true
		return fs.ErrPermission
	}
	return nil
}
func (c *confinedFS) mkdirAll(p string, perm fs.FileMode) error {
	r, n, e := c.resolve(p)
	if e == nil {
		var done func()
		_, done, e = descend(r, strings.Split(filepath.ToSlash(n), "/"), true, perm)
		if e == nil {
			done()
		}
	}
	if e != nil {
		c.failed = true
		return fs.ErrPermission
	}
	return nil
}

// isGitName reports whether a path component names git metadata. On Windows
// the file system ignores trailing dots and spaces and treats a colon as the
// start of a stream name, so those forms are folded away before comparing.
func isGitName(part string) bool {
	if runtime.GOOS == "windows" {
		if i := strings.IndexByte(part, ':'); i >= 0 {
			part = part[:i]
		}
		part = strings.TrimRight(part, ". ")
	}
	return strings.EqualFold(part, ".git")
}

// isGitEntry reports whether info is the object that the ".git" entry of dir
// refers to, whatever name it was reached by (an 8.3 short name, a hard link,
// a differently cased spelling).
func isGitEntry(dir *os.Root, info fs.FileInfo) bool {
	if g, err := dir.Lstat(".git"); err == nil && os.SameFile(g, info) {
		return true
	}
	if g, err := dir.Stat(".git"); err == nil && os.SameFile(g, info) {
		return true
	}
	return false
}

// descend walks dirs below root one pinned directory handle at a time. Each
// directory that was actually opened is compared with the ".git" entry of its
// parent, so the decision rests on the object opened and not on its spelling.
// With create set, missing directories are made. The caller must call done.
func descend(root *os.Root, dirs []string, create bool, perm fs.FileMode) (*os.Root, func(), error) {
	cur, owned := root, false
	done := func() {
		if owned {
			_ = cur.Close()
		}
	}
	for _, part := range dirs {
		if part == "." || part == "" {
			continue
		}
		if isGitName(part) {
			done()
			return nil, func() {}, fs.ErrPermission
		}
		next, err := cur.OpenRoot(part)
		if err != nil && create && errors.Is(err, fs.ErrNotExist) {
			if err = cur.Mkdir(part, perm); err == nil || errors.Is(err, fs.ErrExist) {
				next, err = cur.OpenRoot(part)
			}
		}
		if err == nil {
			var info fs.FileInfo
			if info, err = next.Stat("."); err == nil && isGitEntry(cur, info) {
				err = fs.ErrPermission
			}
			if err != nil {
				_ = next.Close()
			}
		}
		if err != nil {
			done()
			return nil, func() {}, err
		}
		done()
		cur, owned = next, true
	}
	return cur, done, nil
}

// writeGuarded replaces the contents of the file rel under root. The directory
// chain and the opened file are both checked against git metadata by identity
// on the handles that perform the write.
func writeGuarded(root *os.Root, rel string, b []byte, perm fs.FileMode) error {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	base := parts[len(parts)-1]
	if base == "." || isGitName(base) {
		return fs.ErrPermission
	}
	dir, done, err := descend(root, parts[:len(parts)-1], false, 0)
	if err != nil {
		return err
	}
	defer done()
	f, err := dir.OpenFile(base, os.O_WRONLY|os.O_CREATE, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() || isGitEntry(dir, info) {
		return fs.ErrPermission
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.Write(b)
	return err
}
func (c *confinedFS) readDir(p string) ([]fs.DirEntry, error) {
	r, n, e := c.resolve(p)
	if e == nil {
		var f *os.File
		f, e = r.Open(n)
		if e == nil {
			defer f.Close()
			var entries []fs.DirEntry
			entries, e = f.ReadDir(-1)
			if e == nil {
				return entries, nil
			}
		}
	}
	c.failed = true
	return nil, fs.ErrPermission
}
func (c *confinedFS) walkDir(p string, fn fs.WalkDirFunc) error {
	r, n, e := c.resolve(p)
	if e != nil {
		c.failed = true
		return e
	}
	// Opening the search directory validates even a symlink root before walking.
	sub, e := r.OpenRoot(n)
	if e != nil {
		c.failed = true
		return e
	}
	defer sub.Close()
	return fs.WalkDir(sub.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		return fn(filepath.Join(p, filepath.FromSlash(rel)), d, err)
	})
}
func resolvePath(tc *actool.ToolContext, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(tc.WorkingDir, p)
}
func maxOut(tc *actool.ToolContext) int {
	if tc != nil && tc.MaxOutputChars > 0 {
		return tc.MaxOutputChars
	}
	return 30000
}
func capture(tc *actool.ToolContext, s string) string {
	copy := *tc
	copy.OutputDir = ""
	return actool.Capture(&copy, s)
}
