package agents

import (
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

var (
	// DefaultMaxHistoryItems is the fallback limit for history pruning.
	// It can be updated globally by the engine.
	DefaultMaxHistoryItems = 40
)

// UpdateFixHistory is an AfterAgentCallback that captures the agent's summary of what was done
// and appends it to the session state for the next iteration.
func UpdateFixHistory(ctx agent.CallbackContext) (*genai.Content, error) {
	// 1. Get current state
	var currentHistory string
	val, err := ctx.State().Get("fix_history")
	if err == nil {
		if s, ok := val.(string); ok {
			currentHistory = s
		}
	}

	// 2. Extract agent's last response from the session events
	var lastAgentResponse string
	if invCtx, ok := ctx.(agent.InvocationContext); ok {
		events := invCtx.Session().Events()
		for event := range events.All() {
			if event.Author == ctx.AgentName() && event.Content != nil {
				for _, part := range event.Content.Parts {
					if part.Text != "" {
						lastAgentResponse = part.Text
					}
				}
			}
		}
	}

	if lastAgentResponse == "" {
		return nil, nil
	}

	// 3. Update history (keep it concise, e.g., first 500 chars or first paragraph)
	newEntry := fmt.Sprintf("\n- Attempt at %s: %s\n", time.Now().Format("15:04:05"), lastAgentResponse)
	if len(currentHistory) > 5000 {
		// Basic trimming to keep state size under control
		currentHistory = currentHistory[len(currentHistory)-4000:]
	}

	err = ctx.State().Set("fix_history", currentHistory+newEntry)
	return nil, err
}

// SaveSummarizerOutput is an AfterAgentCallback that saves the agent's response
// to a specific state variable so it can be injected into subsequent agents' prompts.
func SaveSummarizerOutput(ctx agent.CallbackContext) (*genai.Content, error) {
	var lastAgentResponse string
	if invCtx, ok := ctx.(agent.InvocationContext); ok {
		events := invCtx.Session().Events()
		for event := range events.All() {
			if event.Author == ctx.AgentName() && event.Content != nil {
				for _, part := range event.Content.Parts {
					if part.Text != "" {
						lastAgentResponse = part.Text
					}
				}
			}
		}
	}

	if lastAgentResponse == "" {
		return nil, nil
	}

	slog.Info("Saving summarizer output to session state", "agent", ctx.AgentName())
	return nil, ctx.State().Set("summarizer_summary", lastAgentResponse)
}

// LogTokenUsage is an AfterModelCallback that logs the token usage of an LLM call.
func LogTokenUsage(ctx agent.CallbackContext, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error) {
	if llmResponse != nil && llmResponse.UsageMetadata != nil {
		slog.Info("LLM Usage",
			"agent", ctx.AgentName(),
			"prompt_tokens", llmResponse.UsageMetadata.PromptTokenCount,
			"candidates_tokens", llmResponse.UsageMetadata.CandidatesTokenCount,
			"total_tokens", llmResponse.UsageMetadata.TotalTokenCount,
		)
	}
	return llmResponse, llmResponseError
}

// PruneHistory is a BeforeModelCallback that reduces the size of the conversation history.
// It removes old tool outputs if the history gets too long.
func PruneHistory(ctx agent.CallbackContext, llmRequest *model.LLMRequest) (*model.LLMResponse, error) {
	maxHistoryItems := DefaultMaxHistoryItems
	val, err := ctx.State().Get("max_history_items")
	if err == nil {
		if v, ok := val.(int); ok {
			maxHistoryItems = v
		} else if v, ok := val.(uint); ok {
			maxHistoryItems = int(v)
		}
	}

	if len(llmRequest.Contents) > maxHistoryItems {
		// Keep the first item (system instruction/initial prompt) and the last N items.
		newContents := make([]*genai.Content, 0, maxHistoryItems+1)
		newContents = append(newContents, llmRequest.Contents[0])
		newContents = append(newContents, llmRequest.Contents[len(llmRequest.Contents)-maxHistoryItems:]...)
		llmRequest.Contents = newContents
		slog.Info("Pruned history to stay within reasonable limits", "agent", ctx.AgentName(), "limit", maxHistoryItems, "remaining", len(llmRequest.Contents))
	}
	return nil, nil
}
