# ACP LLM Provider for ADK

This package implements a bridge between the **Google ADK (Agent Development Kit)** model interface and the **Agent Client Protocol (ACP)**.

## Emulation Overview
The ADK treats LLMs as stateless functions that receive the full conversation history (`genai.Content`) with every request. In contrast, ACP is a stateful protocol where a 'Session' maintains its own history, and subsequent `Prompt` calls append new blocks to that history.

## Key Subtleties & Session Management

1.  **Delta-based Prompting**: To avoid token bloat and history duplication on the ACP side, this layer tracks the history sent to each ADK session. It only sends the "delta" (new content) in subsequent `Prompt` calls to the stateful ACP session.
2.  **History Pruning & Resets**: ADK agents often use `PruneHistory` callbacks to truncate old messages. When this bridge detects that the incoming history is no longer a strict continuation (a **content-based prefix mismatch**), it automatically resets the stateful ACP session by creating a new one. This ensures the ACP agent's internal state always aligns with the ADK's pruned view. Note that ACP agents (like Kiro) often persist sessions to disk; this bridge focuses on volatile, runtime-aligned session state.
3.  **Turn-based Lifecycle**: ACP follows a turn-based lifecycle. This bridge maps asynchronous notifications (`AgentMessageChunk`, `ToolCall`) back to the ADK's streaming response model, completing the turn when the `Prompt` call itself returns.
4.  **Tool Injection**: Tool definitions are injected as a text block only during the *first* prompt of an ACP session to ensure the agent knows its available capabilities without redundantly repeating them in every turn.
5.  **Prompt Engineering for Reliability**: Because the ACP protocol itself lacks a native mechanism for clients to introduce structured tool definitions (relying instead on built-in agent capabilities or external MCP servers), this bridge must manually expose the ADK's tools via the text prompt. Several techniques are used to ensure reliable performance:
    *   **ReAct Pattern**: Instructs the model to prepend tool calls with a `THOUGHT:` block to encourage planning and reduce hallucinations.
    *   **Explicit Delimiters**: Uses a `CALL TOOL <name> ARGS <json>` pattern for clear separation between reasoning and commands.
    *   **Structured Results**: Wraps tool outputs in explicit XML-like tags (e.g., `<tool_result name="..." status="..." call_id="...">`) to help the model distinguish environment feedback from conversational text and quickly identify failures.
    *   **Deterministic Ordering**: Tools are sorted alphabetically to ensure stable prompts across session resets, aiding in reproducibility and debugging.

## Interaction Examples

### Example Prompt Structure
```markdown
# Available Tools
You have access to the following tools to interact with the environment:

- **edit**: Replaces a string with another string in a file.
- **view**: Reads and returns the content of a specified file.

# Tool Calling Pattern
To use a tool, follow this ReAct (Thought-Action) pattern strictly:

THOUGHT: Briefly explain why you are calling this tool and what you expect to achieve.
CALL TOOL <name> ARGS <json>

Example:
THOUGHT: I need to check the current build status to identify compilation errors.
CALL TOOL run_build ARGS {}

Constraints:
- You must only call one tool at a time.
- Do not include any other text after the tool call until you receive the tool result.
```

### Example Tool Result
```xml
<tool_result name="run_build" status="error" call_id="123">
{"exit_code": 1, "output": "main.go:10: undefined: fmt"}
</tool_result>
```
