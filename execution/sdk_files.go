// Adapted from github.com/Autumn-27/norma v0.4.3 tool/files.go.
// File operations use os.Root; search always uses the SDK Go fallback.
package execution

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	actool "github.com/Autumn-27/norma/tool"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
)

const maxLineLen = 2000

func (c *confinedFS) runRead(_ context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return actool.Result{}, err
	}
	path := resolvePath(tc, in.FilePath)
	info, err := c.stat(path)
	if err != nil {
		return actool.Errorf(fmt.Sprintf("Error reading %s: %v", in.FilePath, err)), nil
	}
	if info.IsDir() {
		return actool.Errorf(fmt.Sprintf("Error: %s is a directory, not a file. Use LS or Glob to list its contents.", in.FilePath)), nil
	}
	f, err := c.open(path)
	if err != nil {
		return actool.Errorf(fmt.Sprintf("Error reading %s: %v", in.FilePath, err)), nil
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 64<<10)
	head, _ := br.Peek(8000)
	if isBinary(head) {
		return actool.Text(fmt.Sprintf("[binary file %s not shown — %d bytes]", in.FilePath, info.Size())), nil
	}
	start := 0
	if in.Offset > 0 {
		start = in.Offset - 1
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 2000
	}
	end := start + limit
	budget := maxOut(tc) + readOutputBonus
	var b strings.Builder
	var scanned int64
	for i := 0; i < end && b.Len() <= budget; i++ {
		line, eof, n, err := readCappedLine(br)
		if err != nil {
			c.failed = true
			return actool.Errorf(fmt.Sprintf("Error reading %s: %v", in.FilePath, fs.ErrPermission)), nil
		}
		if scanned += int64(n); scanned > maxReadScan {
			return actool.Errorf(fmt.Sprintf("Error: the requested range of %s is too far into the file to reach; use Grep to find the line", in.FilePath)), nil
		}
		if i >= start {
			if len(line) > maxLineLen {
				line = line[:maxLineLen] + "… [line truncated]"
			}
			fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
		}
		if eof {
			break
		}
	}
	if b.Len() == 0 {
		if info.Size() == 0 {
			return actool.Text("(file is empty)"), nil
		}
		return actool.Text("(no lines in the requested range)"), nil
	}
	return actool.Text(readCapture(tc, b.String(), start)), nil
}

// readCappedLine reads one line without its newline, keeping at most
// maxLineLen+1 bytes of it so a huge line cannot grow memory. eof is true when
// the final (possibly empty) segment was returned; n counts every byte consumed.
func readCappedLine(br *bufio.Reader) (line string, eof bool, n int, err error) {
	var keep []byte
	for {
		seg, e := br.ReadSlice('\n')
		n += len(seg)
		terminated := len(seg) > 0 && seg[len(seg)-1] == '\n'
		if terminated {
			seg = seg[:len(seg)-1]
		}
		if room := maxLineLen + 1 - len(keep); room > 0 {
			if len(seg) > room {
				seg = seg[:room]
			}
			keep = append(keep, seg...)
		}
		switch {
		case e == nil:
			return string(keep), false, n, nil
		case errors.Is(e, bufio.ErrBufferFull):
			continue
		case errors.Is(e, io.EOF):
			return string(keep), true, n, nil
		default:
			return "", false, n, e
		}
	}
}

// readOutputBonus lifts the Read tool's output budget above the shared default
// (actool.ToolContext.MaxOutputChars): Read gets a little more room before truncating.
const readOutputBonus = 1000

// readCapture bounds Read's line-numbered output. When it exceeds the Read budget
// (MaxToolOutputChars + readOutputBonus) it cuts at a line boundary and tells the
// model the exact offset to continue from — the source file is already on disk, so
// there is no need to spill a copy: the model just pages on with Read(offset=…).
// start is the 0-based line the output began at.
func readCapture(tc *actool.ToolContext, s string, start int) string {
	max := maxOut(tc) + readOutputBonus
	if len(s) <= max {
		return s
	}
	head, headLines := headWithinBudget(s, max)
	nextOffset := start + headLines + 1 // 1-based file line to continue from
	return head + fmt.Sprintf(
		"\n... [Read truncated to fit the output limit. Continue reading the file with Read offset=%d (and an optional limit), or narrow with Grep/`sed -n`.] ...",
		nextOffset)
}

// headWithinBudget returns the largest whole-line prefix of s within max bytes and
// the number of lines it contains. Falls back to a hard cut if a single line
// already exceeds the budget (no newline within reach).
func headWithinBudget(s string, max int) (string, int) {
	if len(s) <= max {
		return s, strings.Count(s, "\n")
	}
	cut := strings.LastIndexByte(s[:max], '\n')
	if cut < 0 {
		return s[:max], 0
	}
	head := s[:cut+1]
	return head, strings.Count(head, "\n")
}

// isBinary reports whether data looks like a binary (non-text) file, using the
// common heuristic of a NUL byte in the first chunk.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func (c *confinedFS) runWrite(_ context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return actool.Result{}, err
	}
	path := resolvePath(tc, in.FilePath)
	existed := false
	if info, err := c.stat(path); err == nil {
		if info.IsDir() {
			return actool.Errorf(fmt.Sprintf("Error: %s is an existing directory, not a file.", in.FilePath)), nil
		}
		existed = true
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := c.mkdirAll(dir, 0o755); err != nil {
			return actool.Errorf(fmt.Sprintf("Error creating directory: %v", err)), nil
		}
	}
	if err := c.writeFile(path, []byte(in.Content), 0o644); err != nil {
		return actool.Errorf(fmt.Sprintf("Error writing %s: %v", in.FilePath, err)), nil
	}
	verb := "Created"
	if existed {
		verb = "Overwrote"
	}
	return actool.Text(fmt.Sprintf("%s %s (%d bytes)", verb, in.FilePath, len(in.Content))), nil
}

func (c *confinedFS) runEdit(_ context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		FilePath   string `json:"file_path"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return actool.Result{}, err
	}
	if in.OldString == in.NewString {
		return actool.Errorf("Error: old_string and new_string are identical"), nil
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
	count := strings.Count(content, in.OldString)
	if count == 0 {
		return actool.Errorf(fmt.Sprintf("Error: old_string not found in %s", in.FilePath)), nil
	}
	if count > 1 && !in.ReplaceAll {
		return actool.Errorf(fmt.Sprintf("Error: old_string is not unique in %s (%d matches); add context or set replace_all", in.FilePath, count)), nil
	}
	var updated string
	if in.ReplaceAll {
		updated = strings.ReplaceAll(content, in.OldString, in.NewString)
	} else {
		updated = strings.Replace(content, in.OldString, in.NewString, 1)
	}
	if err := c.writeFile(path, []byte(updated), 0o644); err != nil {
		return actool.Errorf(fmt.Sprintf("Error writing %s: %v", in.FilePath, err)), nil
	}
	return actool.Text(fmt.Sprintf("Edited %s (%d replacement(s))", in.FilePath, count)), nil
}
