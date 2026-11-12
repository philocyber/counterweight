package server

import (
	"bytes"
	"errors"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Workspace file manager — browse / view / edit / download / upload / delete the
// shared work dir (s.m.dir), where all agents write their artifacts. Every path is
// confined to the work dir root (traversal via ".." is neutralised). All routes sit
// behind requireAuth (see Handler()).

const (
	maxWorkspaceRead   = 2 << 20   // 2 MiB: files bigger than this aren't inlined for view/edit (download instead)
	maxWorkspaceUpload = 512 << 20 // 512 MiB per upload request
)

// errNotRegularFile means a workspace entry is a FIFO, device, socket or other
// object that is never read or written inline.
var errNotRegularFile = errors.New("not a regular file")

// Integration credential files stored at the root of the data directory. The
// workspace API never exposes or touches them, nor the temporary files
// privatefs.PublishFile stages next to them.
const (
	githubCredentialFile = "github-token.json"
	jiraCredentialFile   = "jira-credential.json"
)

// wsResolve maps a user-supplied relative path to an absolute path INSIDE the work
// dir. It returns ok=false if the path would escape the root or touches a
// reserved credential entry. filepath.Clean on a rooted copy collapses any ".."
// so nothing can climb above the root.
func (s *Server) wsResolve(rel string) (string, bool) {
	base := filepath.Clean(s.m.dir)
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
	clean := filepath.Clean("/" + rel) // e.g. "/a/../../etc" → "/etc" (still rooted at "/")
	abs := filepath.Clean(filepath.Join(base, clean))
	if abs != base && !strings.HasPrefix(abs, base+string(os.PathSeparator)) {
		return "", false
	}
	if s.wsIsCredential(abs) {
		return "", false
	}
	return abs, true
}

// wsReservedName reports whether an entry name directly under the data directory
// root is reserved: a credential file or a credential staging file. Names match
// case-insensitively and ignore the trailing dots/spaces and ":stream" suffixes
// Windows strips when opening a file.
func wsReservedName(name string) bool {
	name, _, _ = strings.Cut(name, ":")
	name = strings.TrimRight(name, ". ")
	return strings.EqualFold(name, githubCredentialFile) || strings.EqualFold(name, jiraCredentialFile) ||
		strings.HasPrefix(strings.ToLower(name), privatefs.PublicationTempPrefix)
}

// wsIsCredential reports whether abs is, or lies below, a reserved entry of the
// data directory root, whether or not it exists yet. A path component whose
// existing parent resolves to the root (a symlinked alias) counts as a root
// entry, and an existing file that is the same file as a credential (symlink,
// hard link or short name) also matches.
func (s *Server) wsIsCredential(abs string) bool {
	base := filepath.Clean(s.m.dir)
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if wsReservedName(parts[0]) {
		return true
	}
	baseInfo, err := os.Stat(base)
	if err != nil {
		return false
	}
	parent := base
	for i := 1; i < len(parts); i++ {
		parent = filepath.Join(parent, parts[i-1])
		pfi, err := os.Stat(parent)
		if err != nil {
			break // a missing parent cannot alias the root, nor can anything below it
		}
		if wsReservedName(parts[i]) && os.SameFile(pfi, baseInfo) {
			return true
		}
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		return false
	}
	for _, name := range []string{githubCredentialFile, jiraCredentialFile} {
		if cfi, err := os.Stat(filepath.Join(base, name)); err == nil && os.SameFile(fi, cfi) {
			return true
		}
	}
	return false
}

// wsRel renders an absolute path back as a workspace-relative path (forward slashes).
func (s *Server) wsRel(abs string) string {
	base := filepath.Clean(s.m.dir)
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

type wsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // unix millis
}

// GET /api/workspace/list?path=<rel>
func (s *Server) wsList(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.wsResolve(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, 400, "Invalid path")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		writeErr(w, 404, "Path not found")
		return
	}
	if !fi.IsDir() {
		writeErr(w, 400, "Not a directory")
		return
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	out := make([]wsEntry, 0, len(ents))
	for _, e := range ents {
		if s.wsIsCredential(filepath.Join(abs, e.Name())) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, wsEntry{
			Name:  e.Name(),
			Path:  s.wsRel(filepath.Join(abs, e.Name())),
			Dir:   e.IsDir(),
			Size:  info.Size(),
			MTime: info.ModTime().UnixMilli(),
		})
	}
	// Directories first, with each group sorted by name.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, 200, map[string]any{"path": s.wsRel(abs), "entries": out})
}

// GET /api/workspace/read?path=<rel> — inline text for view/edit. Binary or oversize
// files return {binary:true}/{too_large:true} with no content (use download instead).
func (s *Server) wsRead(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.wsResolve(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, 400, "Invalid path")
		return
	}
	// The type check and the bounded read both use the opened object, and the
	// open never blocks on a FIFO.
	f, fi, err := openRegularFile(abs, os.O_RDONLY, 0, false)
	if err != nil {
		wsOpenError(w, abs, err)
		return
	}
	defer f.Close()
	if fi.Size() > maxWorkspaceRead {
		writeJSON(w, 200, map[string]any{"path": s.wsRel(abs), "size": fi.Size(), "too_large": true, "binary": true})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWorkspaceRead+1))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if len(data) > maxWorkspaceRead {
		writeJSON(w, 200, map[string]any{"path": s.wsRel(abs), "size": int64(len(data)), "too_large": true, "binary": true})
		return
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		writeJSON(w, 200, map[string]any{"path": s.wsRel(abs), "size": fi.Size(), "binary": true})
		return
	}
	writeJSON(w, 200, map[string]any{"path": s.wsRel(abs), "size": fi.Size(), "binary": false, "content": string(data)})
}

// wsOpenError answers a failed regular-file open with the matching status.
func wsOpenError(w http.ResponseWriter, abs string, err error) {
	// A directory opens but is not a regular file, so it is told apart first.
	if fi, statErr := os.Stat(abs); statErr == nil && fi.IsDir() {
		writeErr(w, 400, "Cannot read a directory as a file")
		return
	}
	if errors.Is(err, errNotRegularFile) {
		writeErr(w, 400, "Only regular files can be opened")
		return
	}
	writeErr(w, 404, "File not found")
}

// POST /api/workspace/write  {path, content} — create/overwrite a text file.
func (s *Server) wsWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	abs, ok := s.wsResolve(req.Path)
	if !ok || abs == filepath.Clean(s.m.dir) {
		writeErr(w, 400, "Invalid path")
		return
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		writeErr(w, 400, "The target is a directory")
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		writeError(w, 500, err)
		return
	}
	out, _, err := openRegularFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644, false)
	if err != nil {
		if errors.Is(err, errNotRegularFile) {
			writeErr(w, 400, "Only regular files can be opened")
			return
		}
		writeError(w, 500, err)
		return
	}
	_, err = out.WriteString(req.Content)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": s.wsRel(abs)})
}

// POST /api/workspace/mkdir  {path}
func (s *Server) wsMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	abs, ok := s.wsResolve(req.Path)
	if !ok || abs == filepath.Clean(s.m.dir) {
		writeErr(w, 400, "Invalid path")
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "path": s.wsRel(abs)})
}

// DELETE /api/workspace/delete?path=<rel> — removes a file or a directory tree
// (confined to the work dir; the root itself can't be deleted).
func (s *Server) wsDelete(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.wsResolve(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, 400, "Invalid path")
		return
	}
	if abs == filepath.Clean(s.m.dir) {
		writeErr(w, 400, "Cannot delete the workspace root directory")
		return
	}
	if _, err := os.Stat(abs); err != nil {
		writeErr(w, 404, "Path not found")
		return
	}
	if err := os.RemoveAll(abs); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET /api/workspace/download?path=<rel> — stream a file as an attachment.
func (s *Server) wsDownload(w http.ResponseWriter, r *http.Request) {
	abs, ok := s.wsResolve(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, 400, "Invalid path")
		return
	}
	f, fi, err := openRegularFile(abs, os.O_RDONLY, 0, false)
	if err != nil {
		wsDownloadError(w, abs, err)
		return
	}
	defer f.Close()
	name := filepath.Base(abs)
	// RFC 5987 filename* keeps non-ASCII names intact; plain filename is the fallback.
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(name)+"\"; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// wsDownloadError keeps the not-found answer for a missing file or directory and
// refuses other object types.
func wsDownloadError(w http.ResponseWriter, abs string, err error) {
	if errors.Is(err, errNotRegularFile) {
		if fi, statErr := os.Stat(abs); statErr != nil || fi.IsDir() {
			writeErr(w, 404, "File not found")
			return
		}
		writeErr(w, 400, "Only regular files can be opened")
		return
	}
	writeErr(w, 404, "File not found")
}

// POST /api/workspace/upload?path=<dir> — multipart form field "file" (one or more).
func (s *Server) wsUpload(w http.ResponseWriter, r *http.Request) {
	dirAbs, ok := s.wsResolve(r.URL.Query().Get("path"))
	if !ok {
		writeErr(w, 400, "Invalid path")
		return
	}
	if fi, err := os.Stat(dirAbs); err != nil || !fi.IsDir() {
		writeErr(w, 400, "Target directory not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkspaceUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, 400, locale.Errorf("Could not parse upload or upload exceeds size limit: %w", err))
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeErr(w, 400, "Missing upload file (form field file)")
		return
	}
	saved := 0
	for _, hdr := range files {
		name := filepath.Base(hdr.Filename) // strip any path component
		if name == "" || name == "." || name == ".." {
			continue
		}
		destAbs, okd := s.wsResolve(filepath.Join(s.wsRel(dirAbs), name))
		if !okd {
			continue
		}
		if err := saveUpload(hdr, destAbs); err != nil {
			writeError(w, 500, err)
			return
		}
		saved++
	}
	writeJSON(w, 200, map[string]any{"uploaded": saved})
}

func saveUpload(hdr *multipart.FileHeader, dest string) error {
	src, err := hdr.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, _, err := openRegularFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644, false)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

// sanitizeFilename strips characters unsafe for a Content-Disposition filename token.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\"", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, "\r", "")
	return name
}
