package intercept

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// CanonicalArguments preserves numbers exactly, ignores object key order and
// rejects non-object or trailing input. No shell normalization or prefix match.
func CanonicalArguments(input []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	var value map[string]any
	if err := dec.Decode(&value); err != nil {
		return "", err
	}
	if value == nil {
		return "", fmt.Errorf("arguments must be a JSON object")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", fmt.Errorf("trailing JSON input")
	}
	out, err := json.Marshal(value)
	return string(out), err
}

func SameArguments(a, b []byte) bool {
	ca, err := CanonicalArguments(a)
	if err != nil {
		return false
	}
	cb, err := CanonicalArguments(b)
	return err == nil && ca == cb
}

type callPattern struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func parseCallPattern(pattern string) (callPattern, error) {
	var p callPattern
	dec := json.NewDecoder(bytes.NewBufferString(pattern))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return p, fmt.Errorf("trailing rule input")
	}
	if p.Tool == "" {
		return p, fmt.Errorf("an exact tool name is required")
	}
	if p.Arguments != nil {
		if _, err := CanonicalArguments(p.Arguments); err != nil {
			return p, err
		}
	}
	return p, nil
}

func ValidateCallPattern(pattern string) error { _, err := parseCallPattern(pattern); return err }

func CallPatternMatches(pattern, tool string, input []byte) bool {
	p, err := parseCallPattern(pattern)
	if err != nil || p.Tool != tool {
		return false
	}
	return p.Arguments == nil || SameArguments(p.Arguments, input)
}
