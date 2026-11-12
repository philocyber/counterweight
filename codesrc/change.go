package codesrc

import (
	"bytes"
	"context"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// Change scope. A code review can be limited to one change: a pull request or a
// base/head pair. The server fetches both commits into the review clone, checks
// out the head and computes the changed files and line ranges with git. The
// result is the reviewers' starting focus and the basis of each finding's
// in_change flag; it never narrows what the reviewers may read.
//
// Every git call goes through newGitCmd: hooks, external diff drivers, textconv
// and the file-system monitor are off, so nothing the repository carries runs.

// Caps that keep a huge change bounded. Anything cut is reported as truncated.
const (
	MaxChangedFiles  = 300       // files kept in the change list
	MaxFileRanges    = 500       // line ranges kept per file before the file counts as wholly changed
	MaxDiffBytes     = 48 * 1024 // diff text kept for the reviewers
	maxListScanBytes = 2 << 20   // raw/numstat output read from git

	maxPullNumber = 1_000_000_000
)

// History fetched with the two commits of a shallow clone, and the one extra
// deepening when they share no ancestor yet. Variables so tests can shrink them.
var (
	fetchDepth = 200
	deepenBy   = 2000
	// maxPatchScanBytes bounds the zero-context patch read to derive line ranges.
	maxPatchScanBytes = 8 << 20
)

const (
	headRef = "refs/counterweight/head"
	baseRef = "refs/counterweight/base"
)

// ChangeRequest names a change: a pull request number (Base is then optional
// context, resolved by the caller when empty) or an explicit Base and Head.
// Base and Head are a branch, tag or full commit id.
type ChangeRequest struct {
	PullNumber int    `json:"pull_number,omitempty"`
	Base       string `json:"base,omitempty"`
	Head       string `json:"head,omitempty"`
}

// Validate checks that the request names exactly one kind of change and that
// every ref is a safe git argument.
func (r ChangeRequest) Validate() error {
	if r.PullNumber < 0 || r.PullNumber > maxPullNumber {
		return locale.NewError("The pull request number is not valid")
	}
	if r.PullNumber == 0 && (r.Base == "" || r.Head == "") {
		return locale.NewError("A change needs a pull request number, or both a base and a head")
	}
	for _, ref := range []string{r.Base, r.Head} {
		if ref != "" {
			if err := validRef(ref); err != nil {
				return err
			}
		}
	}
	return nil
}

var pullPath = regexp.MustCompile(`^(.*)/pull/([0-9]{1,10})/?$`)

// ParseChangeTarget reads the target of a review command: owner/repo#123, a
// pull request URL, or owner/repo with a separate base..head argument. It
// returns the repository text without the change part and the change, or nil
// when the text names a plain repository. A numeric #fragment is a pull
// request number, not a branch.
func ParseChangeTarget(repo, rangeArg string) (string, *ChangeRequest, error) {
	rest := repo
	var req *ChangeRequest
	if m := pullPath.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(repo), "/")); m != nil {
		n, _ := strconv.Atoi(m[2])
		rest, req = m[1], &ChangeRequest{PullNumber: n}
	} else if i := strings.LastIndexByte(repo, '#'); i >= 0 && allDigits(repo[i+1:]) {
		n, _ := strconv.Atoi(repo[i+1:])
		rest, req = repo[:i], &ChangeRequest{PullNumber: n}
	}
	if rangeArg != "" {
		if req != nil {
			return "", nil, locale.NewError("Give either a pull request or a base..head range, not both")
		}
		base, head, ok := strings.Cut(rangeArg, "...")
		if !ok {
			base, head, ok = strings.Cut(rangeArg, "..")
		}
		if !ok || base == "" || head == "" {
			return "", nil, locale.NewError("A range must look like base..head")
		}
		req = &ChangeRequest{Base: base, Head: head}
	}
	if req != nil {
		if err := req.Validate(); err != nil {
			return "", nil, err
		}
	}
	return rest, req, nil
}

func allDigits(s string) bool {
	if s == "" || len(s) > 10 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// LineRange is an inclusive range of head-side line numbers.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// ChangedFile is one file of the change.
type ChangedFile struct {
	Path    string `json:"path"`
	OldPath string `json:"old_path,omitempty"` // renamed or copied from
	Status  string `json:"status"`             // added, modified, deleted, renamed, copied, type_changed
	Binary  bool   `json:"binary,omitempty"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	// Ranges are the changed head-side lines. A pure deletion is recorded as the
	// two lines around the removed text.
	Ranges []LineRange `json:"ranges,omitempty"`
	// Whole means the ranges were not computed (diff cap, too many hunks): the
	// whole file counts as changed.
	Whole bool `json:"whole,omitempty"`
}

// ChangeSet is the computed change between a base and a head.
type ChangeSet struct {
	BaseRef    string `json:"base_ref,omitempty"`
	BaseCommit string `json:"base_commit"`
	HeadRef    string `json:"head_ref,omitempty"`
	HeadCommit string `json:"head_commit"`
	MergeBase  string `json:"merge_base"`
	PullNumber int    `json:"pull_number,omitempty"`
	PullURL    string `json:"pull_url,omitempty"`

	TotalFiles     int           `json:"total_files"`
	Files          []ChangedFile `json:"files,omitempty"`
	FilesTruncated bool          `json:"files_truncated,omitempty"`
	DiffTruncated  bool          `json:"diff_truncated,omitempty"`
	// Diff is the unified diff (merge base to head) kept for the reviewers; it is
	// stored apart from the change record and never serialized.
	Diff string `json:"-"`
}

// Contains reports whether head-side lines start..end of file fall inside the
// change. A file that is not listed is outside, unless the list was truncated:
// then it cannot be told and counts as inside, so a gate errs towards reporting.
func (c *ChangeSet) Contains(file string, start, end int) bool {
	if c == nil {
		return false
	}
	file = normalizeRepoPath(file)
	if file == "" {
		return false
	}
	if end < start {
		end = start
	}
	for i := range c.Files {
		f := &c.Files[i]
		if f.Path != file {
			continue
		}
		switch {
		case f.Status == "deleted":
			return false
		case f.Whole:
			return true
		}
		for _, r := range f.Ranges {
			if start <= r.End && end >= r.Start {
				return true
			}
		}
		return false
	}
	return c.FilesTruncated
}

func normalizeRepoPath(p string) string {
	p = strings.TrimPrefix(path.Clean(filepath.ToSlash(strings.TrimSpace(p))), "./")
	if p == "." || p == "" {
		return ""
	}
	return p
}

// FetchChangeOptions configures FetchChange.
type FetchChangeOptions struct {
	Repo Repo
	// Dir is the review clone. The change is fetched into it and the head is
	// checked out there.
	Dir string
	// Request names the change. Base must be set; for a pull request Head is
	// ignored (the head is refs/pull/<n>/head).
	Request ChangeRequest
	// Token authenticates the fetch for a private repository, as a
	// per-invocation header; never in the command line or the URL.
	Token string
	// Run executes the network git command (the fetch). nil runs real git with
	// the hardened configuration. Injected in tests, like CloneOptions.Run.
	Run func(ctx context.Context, name string, args []string, env []string, dir string) error
}

// FetchChange fetches the base and the head of a change into the review clone,
// checks the head out and computes the change with git.
func FetchChange(ctx context.Context, opts FetchChangeOptions) (*ChangeSet, error) {
	req := opts.Request
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Base == "" {
		return nil, locale.NewError("A change needs a base")
	}
	dir := opts.Dir
	run := opts.Run
	if run == nil {
		run = func(ctx context.Context, _ string, args []string, env []string, dir string) error {
			_, err := runGitCmd(newGitCmd(ctx, dir, env, args...), args[0])
			return err
		}
	}
	var env []string
	if opts.Token != "" {
		env = basicAuthConfigEnv(opts.Repo.CloneURL(), opts.Token)
	}
	headSrc := req.Head
	if req.PullNumber > 0 {
		headSrc = "refs/pull/" + strconv.Itoa(req.PullNumber) + "/head"
	}
	refspecs := []string{"+" + headSrc + ":" + headRef, "+" + req.Base + ":" + baseRef}
	fetch := func(depthArg string) error {
		args := []string{"fetch", "--no-tags"}
		if depthArg != "" {
			args = append(args, depthArg)
		}
		args = append(args, "--", opts.Repo.CloneURL())
		args = append(args, refspecs...)
		if err := run(ctx, "git", args, env, dir); err != nil {
			// Some git errors echo the failing configuration; never surface it.
			return locale.Errorf("git fetch of the change from %s failed", opts.Repo.Slug())
		}
		return nil
	}

	shallow := false
	if out, err := git(ctx, dir, "rev-parse", "--is-shallow-repository"); err == nil {
		shallow = strings.TrimSpace(out) == "true"
	}
	depthArg := ""
	if shallow {
		depthArg = "--depth=" + strconv.Itoa(fetchDepth)
	}
	if err := fetch(depthArg); err != nil {
		return nil, err
	}
	head, err := resolveCommit(ctx, dir, headRef)
	if err != nil {
		return nil, err
	}
	base, err := resolveCommit(ctx, dir, baseRef)
	if err != nil {
		return nil, err
	}
	if _, err := mergeBase(ctx, dir, base, head); err != nil && shallow {
		// The two tips are further apart than the shallow history reaches.
		if err := fetch("--deepen=" + strconv.Itoa(deepenBy)); err != nil {
			return nil, err
		}
	}
	if _, err := git(ctx, dir, "checkout", "--detach", "--force", "--quiet", head); err != nil {
		return nil, locale.NewError("Could not check out the head of the change")
	}
	change, err := ComputeChange(ctx, dir, base, head)
	if err != nil {
		return nil, err
	}
	change.BaseRef = req.Base
	if req.PullNumber > 0 {
		change.PullNumber = req.PullNumber
		change.HeadRef = "pull/" + strconv.Itoa(req.PullNumber)
	} else {
		change.HeadRef = req.Head
	}
	return change, nil
}

func resolveCommit(ctx context.Context, dir, rev string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	sha := strings.TrimSpace(out)
	if err != nil || !commitSHA.MatchString(sha) {
		return "", locale.NewError("The base or the head of the change could not be resolved")
	}
	return sha, nil
}

func mergeBase(ctx context.Context, dir, base, head string) (string, error) {
	out, err := git(ctx, dir, "merge-base", base, head)
	sha := strings.TrimSpace(out)
	if err != nil || !commitSHA.MatchString(sha) {
		return "", locale.NewError("The base and the head of the change have no common ancestor in the fetched history")
	}
	return sha, nil
}

// ComputeChange computes the change from the merge base of base and head to
// head, which is what a pull request shows. base and head are full commit ids
// present in dir.
func ComputeChange(ctx context.Context, dir, base, head string) (*ChangeSet, error) {
	if !commitSHA.MatchString(base) || !commitSHA.MatchString(head) {
		return nil, locale.NewError("The base and the head must be full commit ids")
	}
	mb, err := mergeBase(ctx, dir, base, head)
	if err != nil {
		return nil, err
	}
	// Renames are detected; external drivers, textconv and colours are never used.
	diff := func(extra ...string) []string {
		args := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "--src-prefix=a/", "--dst-prefix=b/"}
		return append(append(args, extra...), mb, head, "--")
	}
	rawOut, rawCut, err := gitCapped(ctx, dir, maxListScanBytes, diff("--raw", "-z")...)
	if err != nil {
		return nil, err
	}
	numOut, _, err := gitCapped(ctx, dir, maxListScanBytes, diff("--numstat", "-z")...)
	if err != nil {
		return nil, err
	}
	files := parseChangeRaw(rawOut, rawCut)
	applyChangeNumstat(files, numOut)

	c := &ChangeSet{BaseCommit: base, HeadCommit: head, MergeBase: mb, TotalFiles: len(files)}
	if rawCut {
		c.FilesTruncated = true
	}
	patch, patchCut, err := gitCapped(ctx, dir, maxPatchScanBytes, diff("-U0")...)
	if err != nil {
		return nil, err
	}
	applyChangeRanges(files, patch, patchCut)
	if len(files) > MaxChangedFiles {
		files, c.FilesTruncated = files[:MaxChangedFiles], true
	}
	c.Files = files

	text, textCut, err := gitCapped(ctx, dir, MaxDiffBytes, diff("-U3")...)
	if err != nil {
		return nil, err
	}
	if textCut {
		if i := strings.LastIndexByte(text, '\n'); i >= 0 {
			text = text[:i+1]
		}
		c.DiffTruncated = true
	}
	c.Diff = strings.ToValidUTF8(strings.ReplaceAll(text, "\x00", ""), "�")
	return c, nil
}

// parseChangeRaw reads `git diff --raw -z -M`: ":old new oldsha newsha status\0path\0",
// with two paths after a rename or copy. A cut-off output drops the last entry.
func parseChangeRaw(out string, cut bool) []ChangedFile {
	tokens := strings.Split(out, "\x00")
	if cut && len(tokens) > 0 {
		tokens = tokens[:len(tokens)-1]
	}
	var files []ChangedFile
	for i := 0; i < len(tokens); {
		meta := strings.Fields(strings.TrimPrefix(tokens[i], ":"))
		if !strings.HasPrefix(tokens[i], ":") || len(meta) < 5 {
			i++
			continue
		}
		status := meta[4]
		f := ChangedFile{Status: changeStatus(status)}
		switch status[0] {
		case 'R', 'C':
			if i+2 >= len(tokens) {
				return files
			}
			f.OldPath, f.Path = tokens[i+1], tokens[i+2]
			i += 3
		default:
			if i+1 >= len(tokens) {
				return files
			}
			f.Path = tokens[i+1]
			i += 2
		}
		files = append(files, f)
	}
	return files
}

func changeStatus(code string) string {
	switch code[0] {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	}
	return "modified"
}

// applyChangeNumstat reads `git diff --numstat -z -M`: "added\tdeleted\tpath\0",
// "-" for binary files, and an empty path followed by old and new paths for a rename.
func applyChangeNumstat(files []ChangedFile, out string) {
	byPath := make(map[string]*ChangedFile, len(files))
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); {
		parts := strings.SplitN(tokens[i], "\t", 3)
		if len(parts) != 3 {
			i++
			continue
		}
		p := parts[2]
		i++
		if p == "" {
			if i+1 >= len(tokens) {
				return
			}
			p = tokens[i+1]
			i += 2
		}
		f := byPath[p]
		if f == nil {
			continue
		}
		if parts[0] == "-" || parts[1] == "-" {
			f.Binary = true
			continue
		}
		f.Added, _ = strconv.Atoi(parts[0])
		f.Deleted, _ = strconv.Atoi(parts[1])
	}
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// applyChangeRanges derives head-side line ranges from a zero-context patch.
// Patch sections come in the same order as the raw list; when the count does not
// match, or the patch was cut off, the files it cannot vouch for count as wholly
// changed.
func applyChangeRanges(files []ChangedFile, patch string, cut bool) {
	idx := -1
	ranges := make([][]LineRange, len(files))
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			idx++
		case strings.HasPrefix(line, "@@ ") && idx >= 0 && idx < len(files):
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			r := LineRange{Start: start, End: start + count - 1}
			if count == 0 { // pure deletion: git reports the line before the removed text
				r = LineRange{Start: max(start, 1), End: start + 1}
			}
			ranges[idx] = append(ranges[idx], r)
		}
	}
	sections := idx + 1
	for i := range files {
		f := &files[i]
		if f.Binary || f.Status == "deleted" { // no head-side lines
			continue
		}
		unreliable := sections != len(files) && !cut
		if cut {
			unreliable = i >= sections-1 // the last section seen may be partial
		}
		if unreliable {
			f.Whole = true
			continue
		}
		f.Ranges = mergeLineRanges(ranges[i])
		if len(f.Ranges) > MaxFileRanges {
			f.Ranges, f.Whole = nil, true
		}
	}
}

func mergeLineRanges(in []LineRange) []LineRange {
	if len(in) == 0 {
		return nil
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	out := []LineRange{in[0]}
	for _, r := range in[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End+1 {
			last.End = max(last.End, r.End)
		} else {
			out = append(out, r)
		}
	}
	return out
}

var errOutputCap = errors.New("output cap reached")

// capWriter keeps at most max bytes and then fails the write, which stops git.
type capWriter struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); len(p) > room {
		w.buf.Write(p[:room])
		w.over = true
		return room, errOutputCap
	}
	return w.buf.Write(p)
}

// gitCapped runs git with a hardened configuration and keeps at most max bytes
// of its output; cut reports that more was available.
func gitCapped(ctx context.Context, dir string, max int, args ...string) (out string, cut bool, err error) {
	cmd := newGitCmd(ctx, dir, nil, args...)
	w := &capWriter{max: max}
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	runErr := cmd.Run()
	if w.over {
		return w.buf.String(), true, nil
	}
	if runErr != nil {
		return "", false, locale.Errorf("git %s failed: %v: %s", args[0], runErr, strings.TrimSpace(stderr.String()))
	}
	return w.buf.String(), false, nil
}
