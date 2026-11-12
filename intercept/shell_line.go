package intercept

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

// Limits on what is remembered about interactive shell sessions. A pending
// line longer than the first stops being inspectable; past the second, the
// session whose line changed longest ago is forgotten.
const (
	maxShellLineBytes = 64 << 10
	maxShellSessions  = 64
)

// shellLineState is what is known about the input line of one interactive
// shell session between two calls.
type shellLineState struct {
	// text was sent and has not run yet: the open line, after any earlier
	// lines of a command the shell is still reading.
	text string
	// unknown means the line holds input that was not read as text, so what
	// runs at its next line end is not known.
	unknown bool
}

// shellSendCall holds the fields of a shell_send call that reach the session,
// decoded the way the tool decodes them, and the two fields the gate adds for
// a call that continues a line: the text earlier calls left pending on it,
// and a mark when that line is not inspectable. The tool ignores both.
type shellSendCall struct {
	SessionID string   `json:"session_id,omitempty"`
	Text      string   `json:"text,omitempty"`
	Submit    bool     `json:"submit,omitempty"`
	Keys      []string `json:"keys,omitempty"`
	Hex       []string `json:"hex,omitempty"`
	Paste     string   `json:"paste,omitempty"`
	Before    string   `json:"line_before,omitempty"`
	Unknown   bool     `json:"line_not_inspectable,omitempty"`
}

// shellSendFields are the names above. The tool matches names without regard
// to case and keeps the last value, so the reviewed input holds each once.
var shellSendFields = []string{"session_id", "text", "submit", "keys", "hex", "paste", "line_before", "line_not_inspectable"}

// shellSend is one shell_send call read in the order the session receives it,
// after what was pending on the line: text, Enter when submit is set, keys,
// raw bytes, pasted text.
type shellSend struct {
	// program is the text of the lines the call ends or leaves open, with
	// every line end as a newline.
	program string
	// unknown means a line the call ends or leaves open could not be read.
	unknown bool
	// after is what stays pending on the line once the call has been sent.
	after shellLineState
}

func readShellSend(input []byte) shellSend {
	var call shellSendCall
	failed := json.Unmarshal(input, &call) != nil
	return call.read(failed)
}

// read follows the call byte by byte. failed says that part of the input did
// not decode, which leaves the line not inspectable whatever the rest holds.
func (call shellSendCall) read(failed bool) shellSend {
	var out shellSend
	var program strings.Builder
	var pending []byte
	unknown := call.Unknown
	// Input of this call that is not text leaves the call not inspectable even
	// when an interrupt discards the line again before it ends.
	lose := func() { unknown, out.unknown, pending = true, true, pending[:0] }
	end := func() {
		program.WriteByte('\n')
		if unknown {
			// The line ran without having been read. It may have left the shell
			// inside a quote or another unfinished command, so what follows is
			// not known to start a new line; only an interrupt settles that.
			out.unknown = true
			return
		}
		pending = append(pending, '\n')
	}
	typed := func(text string, pasted bool) {
		for i := 0; i < len(text); i++ {
			c := text[i]
			if c == '\r' || c == '\n' {
				// The terminal takes a carriage return as Enter, like a newline.
				end()
				continue
			}
			program.WriteByte(c)
			if c == '\t' && pasted {
				// A pasted tab is blank space. It is kept as a space, because a
				// typed tab asks the line editor to complete the word.
				c = ' '
			} else if c < 0x20 || c == 0x7f {
				// Control bytes, a typed tab among them, act as key presses.
				lose()
			}
			if !unknown {
				if pending = append(pending, c); len(pending) > maxShellLineBytes {
					lose()
				}
			}
		}
	}
	typed(call.Before, false)
	typed(call.Text, false)
	if call.Submit {
		end()
	}
	for _, key := range call.Keys {
		switch shellKey(key) {
		case "enter", "return":
			end()
		case "ctrl+c":
			// The interrupt discards the line, and with it every earlier line of
			// a command the shell was still reading.
			program.WriteByte('\n')
			unknown, pending = false, pending[:0]
		case "ctrl+u":
			// Erases the open line up to the cursor. That is all of it only
			// while everything on the line was typed here, with the cursor
			// left at its end.
			if !unknown {
				program.WriteByte('\n')
				pending = pending[:bytes.LastIndexByte(pending, '\n')+1]
			}
		case "left", "right", "home", "end":
			// An empty line has nothing to move within. On any other line the
			// next input lands at a place that is not followed here.
			if unknown || (len(pending) > 0 && pending[len(pending)-1] != '\n') {
				lose()
			}
		default:
			// History recall, completion, deletion, or a key not known here.
			lose()
		}
	}
	if len(call.Hex) > 0 {
		lose()
	}
	typed(call.Paste, true)
	out.program = program.String()
	if unknown || failed {
		out.unknown, out.after = true, shellLineState{unknown: true}
		return out
	}
	if n := bytes.LastIndexByte(pending, '\n') + 1; n > 0 && !shellKeepsReading(string(pending[:n])) {
		pending = pending[n:]
	}
	out.after.text = string(pending)
	return out
}

// shellKey is a key name as the tool reads it.
func shellKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// shellKeepsReading reports whether a shell that was sent these lines is still
// reading the same command: the last line ends in a continuation, or a quote,
// here-document or compound command is not finished.
func shellKeepsReading(lines string) bool {
	if strings.HasSuffix(lines, "\\\n") {
		return true
	}
	_, err := syntax.NewParser().Parse(strings.NewReader(lines), "")
	return err != nil && syntax.IsIncomplete(err)
}

// shellSendOnLine returns the call as the gate reviews it: with the text that
// is pending on the session's line, and with a mark when that line is not
// inspectable. The call's own fields keep their values, so both what was on
// the line and what the call adds can be read. A call on an empty line is
// returned unchanged.
func shellSendOnLine(input []byte, line shellLineState) []byte {
	var fields map[string]json.RawMessage
	var call shellSendCall
	if json.Unmarshal(input, &fields) != nil || fields == nil {
		// Input that is not an object is reviewed as not inspectable as it is.
		return input
	}
	// A field of the wrong type leaves the others decoded, here and in the
	// tool, which still sends them. Such a call is shown its line as well, and
	// keeps its own fields as sent so that it is still read as not inspectable.
	failed := json.Unmarshal(input, &call) != nil
	// An interrupt sent before anything else discards whatever the line held,
	// so such a call is reviewed like one on an empty line.
	if !failed && call.Text == "" && !call.Submit && len(call.Keys) > 0 && shellKey(call.Keys[0]) == "ctrl+c" {
		line = shellLineState{}
	}
	// Only the gate says what is pending: the same fields in the call itself
	// are dropped.
	if line == (shellLineState{}) && call.Before == "" && !call.Unknown {
		return input
	}
	call.Before, call.Unknown = line.text, line.unknown
	var reviewed map[string]json.RawMessage
	if json.Unmarshal(plainJSON(call), &reviewed) != nil {
		return input
	}
	replaced := func(field string) bool {
		return !failed || field == "line_before" || field == "line_not_inspectable"
	}
	for name := range fields {
		for _, field := range shellSendFields {
			if strings.EqualFold(name, field) && replaced(field) {
				delete(fields, name)
			}
		}
	}
	for name, value := range reviewed {
		if replaced(name) {
			fields[name] = value
		}
	}
	return plainJSON(fields)
}

// plainJSON encodes without rewriting <, > and &, so that rules written for
// the text of a command still find it.
func plainJSON(value any) []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// shellLines remembers the input lines of the interactive shell sessions of
// one run. The zero value is ready to use.
type shellLines struct {
	mu    sync.Mutex
	lines map[string]shellLineEntry
	clock uint64
	// forgot is set once a session was dropped to stay within the limit. From
	// then on a session that is not remembered may be one that was dropped,
	// so its line counts as not inspectable instead of empty.
	forgot bool
}

type shellLineEntry struct {
	shellLineState
	changed uint64
}

func (s *shellLines) get(session string) shellLineState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.lines[session]; ok {
		return entry.shellLineState
	}
	return shellLineState{unknown: s.forgot}
}

func (s *shellLines) set(session string, line shellLineState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if line == (shellLineState{}) && !s.forgot {
		delete(s.lines, session)
		return
	}
	if s.lines == nil {
		s.lines = make(map[string]shellLineEntry)
	}
	s.clock++
	s.lines[session] = shellLineEntry{line, s.clock}
	for len(s.lines) > maxShellSessions {
		oldest, changed := "", s.clock
		for name, entry := range s.lines {
			if entry.changed < changed {
				oldest, changed = name, entry.changed
			}
		}
		delete(s.lines, oldest)
		s.forgot = true
	}
}

// drop forgets a session that has ended. Its name is not used again.
func (s *shellLines) drop(session string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lines, session)
}

// shellLinesFor returns where the lines of the run behind ctx are kept, and
// the name of one of its sessions there. Every agent run carries its own
// record, so two runs never share a line even when their session ids are the
// same. A call outside such a run is told apart by task, agent and
// conversation.
func (i *Interceptor) shellLinesFor(ctx context.Context, session string) (*shellLines, string) {
	if t, _ := ctx.Value(traceKey{}).(*Trace); t != nil {
		return &t.shell, session
	}
	taskID, agentName := taskInfoFromCtx(ctx)
	return &i.shell, taskID + "\x00" + agentName + "\x00" + strconv.FormatInt(ConvIDFromContext(ctx), 10) + "\x00" + session
}

// ShellSendOnLine returns a shell_send call as the gate reviews, records and
// asks about it: together with the text earlier calls left pending on the
// same session's line. The session is the one the tool will write to; an id
// that is missing names no session there either. The tool still receives the
// call as it was made.
func (i *Interceptor) ShellSendOnLine(ctx context.Context, input []byte) (context.Context, []byte) {
	var call shellSendCall
	_ = json.Unmarshal(input, &call)
	lines, session := i.shellLinesFor(ctx, call.SessionID)
	reviewed := shellSendOnLine(input, lines.get(session))
	if string(reviewed) != string(input) {
		ctx = WithEffectiveInput(ctx, "shell_send", input, reviewed)
	}
	return ctx, reviewed
}

// ShellToolRan follows an interactive shell call that ran without error. Only
// then did its input reach the session, so only then does the pending line
// change: a call that was denied, is waiting for a person or failed leaves it
// as it was, and a call approved earlier counts once, when it runs.
func (i *Interceptor) ShellToolRan(ctx context.Context, tool string, input, result []byte) {
	switch tool {
	case "shell_send":
		var call shellSendCall
		_ = json.Unmarshal(input, &call)
		lines, session := i.shellLinesFor(ctx, call.SessionID)
		lines.set(session, readShellSend(shellSendOnLine(input, lines.get(session))).after)
	case "shell_open":
		// A new session starts on an empty line, whatever an earlier session
		// of the same name left behind.
		var text string
		var opened struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(result, &text) == nil && json.Unmarshal([]byte(text), &opened) == nil && opened.SessionID != "" {
			lines, session := i.shellLinesFor(ctx, opened.SessionID)
			lines.set(session, shellLineState{})
		}
	case "shell_close":
		var closed struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(input, &closed)
		lines, session := i.shellLinesFor(ctx, closed.SessionID)
		lines.drop(session)
	}
}
