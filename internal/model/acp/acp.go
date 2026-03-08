package acp

// Package acp implements an ADK (Agent Development Kit) LLM provider for the
// Agent Client Protocol (ACP). It handles the complexities of bridging
// stateless conversation histories to stateful ACP sessions.
//
// See README.md for detailed architectural and prompt engineering documentation.

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"text/template"

	"github.com/coder/acp-go-sdk"
	"github.com/tsaarni/gitunstuck/internal/tools"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

const toolDefinitionsTmpl = `# Available Tools
You have access to the following tools to interact with the environment:

{{range .Tools}}- **{{.Name}}**: {{.Description}}
{{end}}
# Tool Calling Pattern
To use a tool, follow this ReAct (Thought-Action) pattern strictly:

THOUGHT: Briefly explain why you are calling this tool and what you expect to achieve.
CALL TOOL <name> ARGS <json>

Example:
THOUGHT: I need to check the current build status to identify compilation errors.
CALL TOOL run_build ARGS {}

Constraints:
- You must only call one tool at a time.
- Do not include any other text after the tool call until you receive the tool result.`

const toolResultTmpl = `<tool_result name="{{.Name}}" status="{{.Status}}" call_id="{{.CallID}}">
{{.Payload}}
</tool_result>`

type toolResultData struct {
	Name    string
	Status  string
	CallID  string
	Payload string
}

type toolInfo struct {
	Name        string
	Description string
}

// Config holds the configuration for the ACP LLM provider.
type Config struct {
	// Command is the full command to run an ACP-compatible agent (e.g. "kiro-cli acp").
	Command string
	// WorkingDir is the directory in which the ACP executable is run.
	WorkingDir string
	// ModelName is the specific model to request from the ACP agent.
	ModelName string
	// MaxOutputTokens is the maximum tokens in the LLM response.
	MaxOutputTokens int32
}

// New creates a new ACP LLM provider.
func New(cfg Config) model.LLM {
	return &acpLLM{
		cfg: cfg,
	}
}

type acpLLM struct {
	cfg              Config
	conn             *acp.ClientSideConnection
	cmd              *exec.Cmd
	sessions         sync.Map // Map[string]acp.SessionId (ADK Session ID -> ACP Session ID)
	sessionHistories sync.Map // Map[string][]*genai.Content (ADK Session ID -> History)
	mu               sync.Mutex

	// Event channels for each session to route notifications to the right call
	eventChans sync.Map // Map[acp.SessionId]chan *model.LLMResponse
}

func (m *acpLLM) Name() string {
	return "acp"
}

// GenerateContent implements the model.LLM interface. It bridges the stateless ADK
// content request to the stateful ACP protocol by calculating history deltas.
func (m *acpLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.mu.Lock()
		if m.conn == nil {
			slog.Info("Connecting to ACP agent", "command", m.cfg.Command)
			if err := m.ensureConnection(ctx); err != nil {
				m.mu.Unlock()
				slog.Error("Failed to connect to ACP agent", "error", err)
				yield(nil, fmt.Errorf("failed to connect to ACP agent: %w", err))
				return
			}
		}
		m.mu.Unlock()

		// 1. Session Management: Align ADK session with a stateful ACP session.
		acpSessionID, prevHistory, err := m.getOrCreateACPSession(ctx, req)
		if err != nil {
			yield(nil, err)
			return
		}

		// 2. Prompt Preparation: Map the delta (new messages) to ACP content blocks.
		newContents := req.Contents[len(prevHistory):]
		isFirstPrompt := len(prevHistory) == 0
		promptBlocks := m.toACPContentBlocks(newContents, req.Tools, isFirstPrompt)

		slog.Debug("Sending prompt delta to ACP agent",
			"acp_session_id", acpSessionID,
			"total_history_len", len(req.Contents),
			"new_blocks_count", len(promptBlocks))

		// Update history tracking before sending the prompt.
		adkSessionID := "default"
		if invCtx, ok := ctx.(agent.InvocationContext); ok {
			adkSessionID = invCtx.Session().ID()
		}
		m.sessionHistories.Store(adkSessionID, req.Contents)

		// 3. Prompt Execution: Send the prompt and stream responses back to ADK.
		m.streamLLMResponses(ctx, acpSessionID, promptBlocks, yield)
	}
}

// getOrCreateACPSession ensures an active ACP session exists that is compatible
// with the current ADK request history. If history was pruned, it resets the session.
func (m *acpLLM) getOrCreateACPSession(ctx context.Context, req *model.LLMRequest) (acp.SessionId, []*genai.Content, error) {
	adkSessionID := "default"
	if invCtx, ok := ctx.(agent.InvocationContext); ok {
		adkSessionID = invCtx.Session().ID()
	}

	var prevHistory []*genai.Content
	if val, ok := m.sessionHistories.Load(adkSessionID); ok {
		prevHistory = val.([]*genai.Content)
	}

	isContinuation := isPrefix(req.Contents, prevHistory)
	if sid, ok := m.sessions.Load(adkSessionID); ok && isContinuation {
		return sid.(acp.SessionId), prevHistory, nil
	}

	if !isContinuation && len(prevHistory) > 0 {
		slog.Info("History pruned or changed, resetting ACP session", "adk_session_id", adkSessionID)
	} else {
		slog.Info("Creating new ACP session", "adk_session_id", adkSessionID)
	}

	cwd := m.cfg.WorkingDir
	if cwd == "" {
		cwd = "/"
	}
	resp, err := m.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		slog.Error("Failed to create ACP session", "error", err, "adk_session_id", adkSessionID)
		return "", nil, fmt.Errorf("failed to create ACP session: %w", err)
	}

	m.sessions.Store(adkSessionID, resp.SessionId)
	return resp.SessionId, nil, nil
}

// streamLLMResponses handles the asynchronous turn-based lifecycle of an ACP prompt.
func (m *acpLLM) streamLLMResponses(ctx context.Context, acpSessionID acp.SessionId, promptBlocks []acp.ContentBlock, yield func(*model.LLMResponse, error) bool) {
	// Set up event channel for this specific session prompt
	eventCh := make(chan *model.LLMResponse, 10)
	m.eventChans.Store(acpSessionID, eventCh)
	defer m.eventChans.Delete(acpSessionID)

	// Call Prompt in a goroutine
	errCh := make(chan error, 1)
	usageCh := make(chan *genai.UsageMetadata, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic in ACP prompt goroutine", "error", r)
				errCh <- fmt.Errorf("panic in ACP prompt goroutine: %v", r)
			}
		}()

		resp, err := m.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: acpSessionID,
			Prompt:    promptBlocks,
			Meta: map[string]any{
				"maxOutputTokens": int(m.cfg.MaxOutputTokens),
			},
		})
		if err != nil {
			slog.Error("ACP prompt request failed", "error", err, "acp_session_id", acpSessionID)
		} else if resp.Meta != nil {
			if metaMap, ok := resp.Meta.(map[string]any); ok {
				if usage, ok := metaMap["usage"].(map[string]any); ok {
					m := &genai.UsageMetadata{}
					if pt, ok := usage["promptTokenCount"].(float64); ok {
						m.PromptTokenCount = int32(pt)
					}
					if rt, ok := usage["responseTokenCount"].(float64); ok {
						m.ResponseTokenCount = int32(rt)
					}
					if tt, ok := usage["totalTokenCount"].(float64); ok {
						m.TotalTokenCount = int32(tt)
					}
					usageCh <- m
				}
			}
		}
		errCh <- err
	}()

	var finalUsage *genai.UsageMetadata
	for {
		select {
		case <-ctx.Done():
			slog.Warn("GenerateContent context cancelled", "error", ctx.Err())
			yield(nil, ctx.Err())
			return
		case usage := <-usageCh:
			finalUsage = usage
		case err := <-errCh:
			if err != nil {
				yield(nil, fmt.Errorf("ACP prompt error: %w", err))
			}
			if finalUsage != nil {
				yield(&model.LLMResponse{
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:     finalUsage.PromptTokenCount,
						CandidatesTokenCount: finalUsage.ResponseTokenCount,
						TotalTokenCount:      finalUsage.TotalTokenCount,
					},
					Partial: true,
				}, nil)
			}
			slog.Debug("ACP prompt processing completed", "acp_session_id", acpSessionID)
			return
		case resp := <-eventCh:
			if !yield(resp, nil) {
				slog.Debug("Yield returned false, stopping response stream", "acp_session_id", acpSessionID)
				return
			}
		}
	}
}

// isPrefix checks if prev is a prefix of curr using content-based comparison.
// ADK doesn't guarantee pointer stability across turns, so we check the text content.
func isPrefix(curr, prev []*genai.Content) bool {
	if len(curr) < len(prev) {
		return false
	}
	for i := range prev {
		if !contentsEqual(curr[i], prev[i]) {
			return false
		}
	}
	return true
}

func contentsEqual(a, b *genai.Content) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a.Parts) != len(b.Parts) {
		return false
	}
	for i := range a.Parts {
		if a.Parts[i].Text != b.Parts[i].Text {
			return false
		}
		// Note: We primarily care about text and tool responses for prefix matching.
		if a.Parts[i].Thought != b.Parts[i].Thought {
			return false
		}
	}
	return true
}

// toACPContentBlocks converts ADK's genai.Content items into ACP-compatible ContentBlocks.
// If isFirstPrompt is true, it also prepends a list of available tools.
func (m *acpLLM) toACPContentBlocks(contents []*genai.Content, tools map[string]any, isFirstPrompt bool) []acp.ContentBlock {
	var blocks []acp.ContentBlock

	// If there are tools, we inject them into the prompt (non-structured approach)
	// ONLY if it's the first prompt of the session.
	if isFirstPrompt && len(tools) > 0 {
		toolDesc := m.generateToolDefinitions(tools)
		slog.Debug("Injecting tool definitions into prompt", "tool_count", len(tools))
		blocks = append(blocks, acp.TextBlock(toolDesc))
	}

	for _, c := range contents {
		for _, p := range c.Parts {
			if p.Text != "" {
				blocks = append(blocks, acp.TextBlock(p.Text))
			}
			if p.FunctionResponse != nil {
				// Map tool result back to the agent
				resultJSON, _ := json.Marshal(p.FunctionResponse.Response)
				slog.Debug("Mapping function response to prompt block", "tool", p.FunctionResponse.Name, "id", p.FunctionResponse.ID)

				status := "success"
				if strings.Contains(strings.ToLower(string(resultJSON)), "error") ||
					strings.Contains(strings.ToLower(string(resultJSON)), "failed") {
					status = "error"
				}

				data := toolResultData{
					Name:    p.FunctionResponse.Name,
					Status:  status,
					CallID:  p.FunctionResponse.ID,
					Payload: string(resultJSON),
				}

				tmpl, err := template.New("toolRes").Parse(toolResultTmpl)
				if err != nil {
					slog.Error("Failed to parse tool result template", "error", err)
					continue
				}

				var buf strings.Builder
				if err := tmpl.Execute(&buf, data); err != nil {
					slog.Error("Failed to execute tool result template", "error", err)
					continue
				}

				blocks = append(blocks, acp.TextBlock(buf.String()))
			}
		}
	}
	return blocks
}

// generateToolDefinitions creates a text description of all available tools
// for the LLM. It sorts the tools by name to ensure deterministic output.
func (m *acpLLM) generateToolDefinitions(tools map[string]any) string {
	// Collect and sort names for deterministic output.
	names := slices.Collect(maps.Keys(tools))
	slices.Sort(names)

	var toolInfos []toolInfo
	for _, name := range names {
		desc := ""
		if adkTool, ok := tools[name].(agent.Agent); ok {
			desc = adkTool.Description()
		}
		toolInfos = append(toolInfos, toolInfo{Name: name, Description: desc})
	}

	tmpl, err := template.New("toolDefs").Parse(toolDefinitionsTmpl)
	if err != nil {
		slog.Error("Failed to parse tool definitions template", "error", err)
		return ""
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, struct{ Tools []toolInfo }{Tools: toolInfos}); err != nil {
		slog.Error("Failed to execute tool definitions template", "error", err)
		return ""
	}

	return buf.String()
}

// ensureConnection starts the ACP agent process and performs the initial handshake.
func (m *acpLLM) ensureConnection(ctx context.Context) error {
	parts := strings.Fields(m.cfg.Command)
	if len(parts) == 0 {
		return fmt.Errorf("empty ACP command")
	}
	cmdPath := parts[0]
	cmdArgs := parts[1:]

	slog.Info("Starting ACP agent process", "command", cmdPath, "args", cmdArgs)
	m.cmd = exec.Command(cmdPath, cmdArgs...)
	stdin, err := m.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := m.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := m.cmd.Start(); err != nil {
		return err
	}

	m.conn = acp.NewClientSideConnection(m, stdin, stdout)

	// Initialize
	slog.Debug("Initializing ACP connection", "model", m.cfg.ModelName)
	_, err = m.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		Meta: map[string]any{
			"model": m.cfg.ModelName,
		},
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: true,
		},
	})
	if err != nil {
		slog.Error("ACP initialization failed", "error", err)
	} else {
		slog.Info("ACP connection initialized")
	}
	return err
}

// SessionUpdate implements the acp.Client interface. It routes asynchronous
// notifications from the ACP agent to the correct ADK response channel.
func (m *acpLLM) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	ch, ok := m.eventChans.Load(n.SessionId)
	if !ok {
		slog.Debug("Received SessionUpdate for unregistered session", "acp_session_id", n.SessionId)
		return nil
	}
	eventCh := ch.(chan *model.LLMResponse)

	u := n.Update
	resp := &model.LLMResponse{
		Partial: true,
	}

	switch {
	case u.AgentMessageChunk != nil:
		chunk := u.AgentMessageChunk.Content
		if chunk.Text != nil {
			slog.Debug("Received text chunk from ACP agent", "acp_session_id", n.SessionId, "len", len(chunk.Text.Text))
			resp.Content = &genai.Content{
				Parts: []*genai.Part{{Text: chunk.Text.Text}},
			}
		}
	case u.AgentThoughtChunk != nil:
		chunk := u.AgentThoughtChunk.Content
		if chunk.Text != nil {
			slog.Debug("Received thought chunk from ACP agent", "acp_session_id", n.SessionId, "len", len(chunk.Text.Text))
			resp.Content = &genai.Content{
				Parts: []*genai.Part{{Thought: true, Text: chunk.Text.Text}},
			}
		}
	case u.ToolCall != nil:
		slog.Info("Received tool call from ACP agent", "acp_session_id", n.SessionId, "tool", u.ToolCall.Title, "id", u.ToolCall.ToolCallId)
		// Map ACP tool call to ADK FunctionCall
		args := make(map[string]any)
		if u.ToolCall.RawInput != nil {
			if b, ok := u.ToolCall.RawInput.(json.RawMessage); ok {
				json.Unmarshal(b, &args)
			} else if ma, ok := u.ToolCall.RawInput.(map[string]any); ok {
				args = ma
			}
		}

		resp.Content = &genai.Content{
			Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{
					ID:   string(u.ToolCall.ToolCallId),
					Name: u.ToolCall.Title, // Using Title as Name for mapping
					Args: args,
				},
			}},
		}
	}

	if resp.Content != nil {
		select {
		case eventCh <- resp:
		default:
			slog.Warn("Event channel full, dropping notification", "acp_session_id", n.SessionId)
		}
	}
	return nil
}

// Implement other methods to satisfy Client interface
func (m *acpLLM) ReadTextFile(ctx context.Context, p acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	startLine := 1
	if p.Line != nil {
		startLine = *p.Line
	}
	limit := -1
	if p.Limit != nil {
		limit = *p.Limit
	}

	lines, err := tools.ReadLines(p.Path, startLine, limit)
	if err != nil {
		slog.Error("Failed to read lines from file", "path", p.Path, "error", err)
		return acp.ReadTextFileResponse{}, err
	}

	return acp.ReadTextFileResponse{Content: strings.Join(lines, "\n")}, nil
}

func (m *acpLLM) WriteTextFile(ctx context.Context, p acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if err := tools.WriteFile(p.Path, p.Content, true); err != nil {
		slog.Error("Failed to write file", "path", p.Path, "error", err)
		return acp.WriteTextFileResponse{}, err
	}

	slog.Info("Successfully wrote file", "path", p.Path, "len", len(p.Content))
	return acp.WriteTextFileResponse{}, nil
}
func (m *acpLLM) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(p.Options[0].OptionId)}, nil
}
func (m *acpLLM) CreateTerminal(ctx context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("not implemented")
}
func (m *acpLLM) KillTerminalCommand(ctx context.Context, p acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return acp.KillTerminalCommandResponse{}, fmt.Errorf("not implemented")
}
func (m *acpLLM) TerminalOutput(ctx context.Context, p acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("not implemented")
}
func (m *acpLLM) ReleaseTerminal(ctx context.Context, p acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("not implemented")
}
func (m *acpLLM) WaitForTerminalExit(ctx context.Context, p acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("not implemented")
}
