package locale

// Message retains an explicitly selected built-in display template and raw
// arguments, so transient events can be rendered separately for each subscriber.
// It must never be constructed from arbitrary user content or stored evidence.
type Message struct {
	Template string
	Args     []any
}

func M(english string, args ...any) Message { return Message{english, append([]any(nil), args...)} }
func (m Message) In(lang Lang) string       { return Text(lang, m.Template, m.Args...) }
