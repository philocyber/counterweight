package agent

import (
	"context"
	"iter"
	"strings"

	"github.com/Autumn-27/norma/llm"
)

// Keep run data available after transcript compaction/resume without promoting
// target-controlled observations or model-authored intent text to system roles.
type workerContextProvider struct {
	llm.Provider
	marker string
	data   string
}

func (p workerContextProvider) request(req llm.CompletionRequest) llm.CompletionRequest {
	for _, system := range req.System {
		if strings.Contains(system, p.marker) {
			// Keep Norma's user-role startup reminder first when present. Final
			// checkpoint capture preserves that prefix after replacing history.
			if len(req.Messages) > 0 && req.Messages[0].Role == llm.RoleUser && strings.HasPrefix(req.Messages[0].Text(), "<system-reminder>") {
				messages := append([]llm.Message(nil), req.Messages...)
				messages[0].Content = append(append([]llm.ContentBlock(nil), messages[0].Content...), llm.TextBlock(p.data))
				req.Messages = messages
				break
			}
			messages := make([]llm.Message, 0, len(req.Messages)+1)
			messages = append(messages, llm.UserText(p.data))
			req.Messages = append(messages, req.Messages...)
			break
		}
	}
	return req
}

func (p workerContextProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return p.Provider.Stream(ctx, p.request(req))
}

func (p workerContextProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return p.Provider.Complete(ctx, p.request(req))
}
