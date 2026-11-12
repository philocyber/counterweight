package proxy

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// sseReader wraps an io.Reader to parse SSE events and trigger hooks
type sseReader struct {
	flow     *Flow
	proxy    *Proxy
	reader   *bufio.Reader
	buffer   []byte      // 当前事件累积的数据
	scanned  int         // bytes of buffer already examined for an event boundary
	skipping bool        // dropping an oversized event until its boundary
	started  bool
	ended    bool
	mu       sync.Mutex
}

// maxSSEEventBytes bounds the pending, not yet delimited part of a stream. A
// longer event is dropped (it is still relayed to the client unchanged).
const maxSSEEventBytes = 1 << 20

// newSSEReader creates a new SSE reader wrapper
func newSSEReader(f *Flow, r io.Reader) io.Reader {
	return &sseReader{
		flow:   f,
		proxy:  f.ConnContext.proxy,
		reader: bufio.NewReader(r),
		buffer: make([]byte, 0, 1024),
	}
}

// Read implements io.Reader, parsing SSE events as data is read
func (sr *sseReader) Read(p []byte) (n int, err error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.ended {
		return 0, io.EOF
	}

	// 从底层 reader 读取数据
	n, err = sr.reader.Read(p)
	if n > 0 {
		// 将新数据添加到 buffer
		sr.buffer = append(sr.buffer, p[:n]...)

		// 解析 buffer 中的所有完整事件
		sr.parseEvents()
	}

	if err != nil {
		// 流结束，触发 SSEEnd
		if sr.started {
			sr.flushEvent() // 处理最后一个可能不完整的事件
			sr.triggerEnd()
		}
		return n, err
	}

	if !sr.started {
		sr.started = true
	}

	return n, nil
}

// parseEvents parses SSE events from the buffer
func (sr *sseReader) parseEvents() {
	for {
		eventEnd, delimLen := sr.findEventEnd()
		if eventEnd == -1 {
			// No complete event yet. Bound what is held while waiting.
			if sr.skipping {
				// Keep only a tail so a boundary split across reads is still seen.
				if len(sr.buffer) > 3 {
					sr.buffer = append(sr.buffer[:0], sr.buffer[len(sr.buffer)-3:]...)
				}
				sr.scanned = 0
			} else if len(sr.buffer) > maxSSEEventBytes {
				sr.skipping = true
				sr.buffer = append(sr.buffer[:0], sr.buffer[len(sr.buffer)-3:]...)
				sr.scanned = 0
			}
			break
		}

		if sr.skipping {
			sr.skipping = false
		} else if eventEnd > 0 {
			sr.parseAndFlushEvent(string(sr.buffer[:eventEnd]))
		}

		sr.buffer = sr.buffer[eventEnd+delimLen:]
		sr.scanned = 0
	}
}

// findEventEnd looks for a blank line (LF LF or CRLF CRLF) in the buffer,
// resuming from where the previous call stopped. It returns the start of the
// boundary and its length, or -1.
func (sr *sseReader) findEventEnd() (int, int) {
	for i := sr.scanned; i < len(sr.buffer); i++ {
		if sr.buffer[i] != '\n' {
			continue
		}
		j := i + 1
		if j < len(sr.buffer) && sr.buffer[j] == '\r' {
			j++
		}
		if j >= len(sr.buffer) {
			// Undecided until more data arrives; examine this newline again.
			sr.scanned = i
			return -1, 0
		}
		if sr.buffer[j] == '\n' {
			return i, j + 1 - i
		}
	}
	sr.scanned = len(sr.buffer)
	return -1, 0
}

// parseAndFlushEvent 解析并触发一个事件
func (sr *sseReader) parseAndFlushEvent(eventData string) {
	event := sr.parseEvent(eventData)

	// 只处理有 data 字段的事件
	if event.Data != "" {
		// 保存到 Flow 中
		sr.flow.SSE.addEvent(event)

		// 触发 SSEMessage hook（通过 f.SSE.Events 访问最新事件）
		for _, addon := range sr.proxy.Addons {
			addon.SSEMessage(sr.flow)
		}
	}
}

// flushEvent 解析并触发当前缓冲区中的事件
func (sr *sseReader) flushEvent() {
	if len(sr.buffer) == 0 || sr.skipping {
		return
	}

	// 解析事件
	event := sr.parseEvent(string(sr.buffer))

	// 只处理有 data 字段的事件
	if event.Data != "" {
		// 保存到 Flow 中
		sr.flow.SSE.addEvent(event)

		// 触发 SSEMessage hook（通过 f.SSE.Events 访问最新事件）
		for _, addon := range sr.proxy.Addons {
			addon.SSEMessage(sr.flow)
		}
	}

	// 清空 buffer
	sr.buffer = sr.buffer[:0]
	sr.scanned = 0
}

// parseEvent 解析 SSE 事件文本
func (sr *sseReader) parseEvent(text string) *SSEEvent {
	event := &SSEEvent{
		Event: "message",
		Data:  "",
		Raw:   text,
		Time:  time.Now(),
	}

	// 按行解析
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 跳过注释行
		if strings.HasPrefix(line, ":") {
			continue
		}

		// 解析 field: value 格式
		if idx := strings.Index(line, ":"); idx != -1 {
			field := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])

			switch field {
			case "data":
				// 多个 data 行用换行符连接
				if event.Data != "" {
					event.Data += "\n"
				}
				event.Data += value
			case "event":
				event.Event = value
			case "id":
				event.ID = value
			case "retry":
				if retry, err := strconv.Atoi(value); err == nil {
					event.Retry = retry
				}
			}
		}
	}

	return event
}

// triggerEnd 触发 SSE 结束 hook
func (sr *sseReader) triggerEnd() {
	if sr.ended {
		return
	}

	sr.ended = true

	for _, addon := range sr.proxy.Addons {
		addon.SSEEnd(sr.flow)
	}

	log.Debugf("SSE stream ended for %s", sr.flow.Request.URL.String())
}
