package notify

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Channel adapts one notification transport. Implementations must be stateless: concurrent
// configurations share instances and supply credentials through cfg.
type Channel interface {
	// Kind returns the channel identifier, which must match its registry key.
	Kind() string
	// Validate checks required fields and formats before saving. Errors appear directly to operators, so
	// identify the missing field rather than reporting a generic invalid configuration.
	Validate(cfg map[string]any) error
	// Send delivers a message and returns the number of items actually delivered. Platforms impose size
	// limits; marking an entire truncated batch delivered would silently lose the omitted findings. Mark
	// only the first kept items delivered and retain the rest for the next batch. An error means delivery
	// failed; *PermanentError means it must not be retried. Ignore kept on failure.
	Send(ctx context.Context, cfg map[string]any, m Message) (int, error)
	// DefaultRatePerMin returns the platform's recommended per-minute limit for new configurations. Zero
	// means no known limit.
	DefaultRatePerMin() int
	// SecretKeys identifies credential fields. The API masks them and preserves stored values when masks
	// are submitted. Only each adapter knows its credentials (such as an entire WeCom webhook URL or a
	// DingTalk signing secret); callers must not guess.
	SecretKeys() []string
	// DestinationKeys identifies fields controlling where messages are sent. Changing a destination while
	// retaining credentials could send stored secrets to an attacker and defeat masking. See
	// PrepareConfigUpdate.
	DestinationKeys() []string
}

// registry explicitly lists adapters instead of relying on init side effects. Supported channels
// remain visible in one place and missing implementations surface at compile time.
var registry = map[string]Channel{
	KindDingTalk: dingTalkChannel{},
	KindFeishu:   feishuChannel{},
	KindWeCom:    weComChannel{},
	KindWebhook:  webhookChannel{},
	KindTelegram: telegramChannel{},
	KindEmail:    emailChannel{},
}

// Get retrieves an adapter by kind.
func Get(kind string) (Channel, bool) {
	c, ok := registry[kind]
	return c, ok
}

// ValidKind reports whether a channel kind is supported.
func ValidKind(kind string) bool {
	_, ok := registry[kind]
	return ok
}

// Kinds returns supported kinds in lexical order for stable UI options.
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PermanentError marks non-retryable failures such as invalid credentials, rejection, or malformed
// requests. Retries help transient network failures, rate limits, and 5xx responses; retrying
// permanent failures only obscures the actual cause.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent marks a failure as non-retryable. Nil remains nil, allowing return Permanent(someCheck()).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether the error chain contains a permanent-failure marker.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// Configuration readers normalize JSONB data decoded into map[string]any, with float64 numbers and
// []any arrays. They tolerate form-induced type differences such as string ports.

// cfgString reads a string and trims whitespace commonly introduced by copying from forms.
func cfgString(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// cfgInt accepts both JSON float64 numbers and string values.
func cfgInt(cfg map[string]any, key string) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// cfgBool accepts booleans and textual true/1 values.
func cfgBool(cfg map[string]any, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

// cfgStrings trims array elements and drops empty strings.
func cfgStrings(cfg map[string]any, key string) []string {
	raw, ok := cfg[key].([]any)
	if !ok {
		// Accept a single string for forms containing only one value.
		if s := cfgString(cfg, key); s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cfgMap reads string mappings such as custom headers, trims keys and values, and drops empty keys.
func cfgMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}
