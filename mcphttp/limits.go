package mcphttp

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const maxResponseBytes = 8 * 1024 * 1024
const maxEndpointBytes = 64 * 1024

var errResponseLimit = errors.New("mcp: response exceeds byte limit")

// boundedLine bounds allocation even when no newline arrives. ReadSlice itself
// retains only the fixed reader buffer; the assembled line never exceeds max.
func boundedLine(reader *bufio.Reader, max int) (string, error) {
	if max <= 0 {
		return "", errResponseLimit
	}
	var line strings.Builder
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > max-line.Len() {
			return "", errResponseLimit
		}
		line.Write(part)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line.String(), err
	}
}

type responseBudget struct {
	reader    io.Reader
	remaining int
	exceeded  bool
}

func (b *responseBudget) Read(p []byte) (int, error) {
	if b.exceeded {
		return 0, errResponseLimit
	}
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.reader.Read(p)
	if n > b.remaining {
		b.exceeded = true
		return n, errResponseLimit
	}
	b.remaining -= n
	return n, err
}
