package agents

import (
	"fmt"
	"github.com/tsaarni/gitunstuck/internal/git"
	"github.com/tsaarni/gitunstuck/internal/tools"
	"log/slog"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
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
	const maxHistoryItems = 10
	if len(llmRequest.Contents) > maxHistoryItems {
		// Keep the first item (system instruction/initial prompt) and the last N items.
		newContents := make([]*genai.Content, 0, maxHistoryItems+1)
		newContents = append(newContents, llmRequest.Contents[0])
		newContents = append(newContents, llmRequest.Contents[len(llmRequest.Contents)-maxHistoryItems:]...)
		llmRequest.Contents = newContents
		slog.Debug("Pruned history", "agent", ctx.AgentName(), "remaining", len(llmRequest.Contents))
	}
	return nil, nil
}

// Config defines common configuration for agents.
type Config struct {
	// Model is the LLM to be used by the agent.
	Model model.LLM
	// MaxOutputTokens is the maximum number of tokens in the LLM response.
	MaxOutputTokens int32
	// GitClient is the client used to interact with the Git repository.
	GitClient *git.Client
	// MergeContext provides the static git context and initial unmerged files.
	MergeContext *git.MergeInfo
}

// BaseTools returns the set of tools common to all agents.
func BaseTools() []tool.Tool {
	tl := []tool.Tool{
		tools.NewGitTool(),
		tools.NewGetMergeContextTool(),
		tools.NewViewTool(),
		tools.NewEditTool(),
		tools.NewWriteTool(),
		tools.NewCreateTool(),
	}

	if tools.BuildCommand != "" {
		tl = append(tl, tools.NewRunBuildTool())
	}
	if tools.TestCommand != "" {
		tl = append(tl, tools.NewRunTestsTool())
	}

	return tl
}
