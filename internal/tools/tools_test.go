package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"
)

type mockToolContext struct {
	context.Context
}

func (m *mockToolContext) FunctionCallID() string         { return "" }
func (m *mockToolContext) Actions() *session.EventActions { return &session.EventActions{} }
func (m *mockToolContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (m *mockToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
func (m *mockToolContext) RequestConfirmation(hint string, payload any) error   { return nil }
func (m *mockToolContext) AgentName() string                                    { return "" }
func (m *mockToolContext) ReadonlyState() session.ReadonlyState                 { return nil }
func (m *mockToolContext) State() session.State                                 { return nil }
func (m *mockToolContext) Artifacts() agent.Artifacts                           { return nil }
func (m *mockToolContext) InvocationID() string                                 { return "" }
func (m *mockToolContext) UserContent() *genai.Content                          { return nil }
func (m *mockToolContext) AppName() string                                      { return "" }
func (m *mockToolContext) Branch() string                                       { return "" }
func (m *mockToolContext) SessionID() string                                    { return "" }
func (m *mockToolContext) UserID() string                                       { return "" }

func TestViewTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gitunstuck-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	WorkingDir = tempDir

	filePath := filepath.Join(tempDir, "test.txt")
	content := "line 1\nline 2\nline 3\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ctx := &mockToolContext{Context: context.Background()}

	// Test viewing a file
	res, err := ViewTool(ctx, ViewArgs{Path: "test.txt"})
	if err != nil {
		t.Fatalf("ViewTool failed: %v", err)
	}
	if !strings.Contains(res.Content, "1. line 1") {
		t.Errorf("expected line 1, got %s", res.Content)
	}

	// Test viewing a directory
	res, err = ViewTool(ctx, ViewArgs{Path: "."})
	if err != nil {
		t.Fatalf("ViewTool failed: %v", err)
	}
	if !strings.Contains(res.Content, "test.txt") {
		t.Errorf("expected test.txt in listing, got %s", res.Content)
	}
}

func TestEditTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gitunstuck-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	WorkingDir = tempDir

	filePath := filepath.Join(tempDir, "test.txt")
	content := "hello world\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ctx := &mockToolContext{Context: context.Background()}

	res, err := EditTool(ctx, EditArgs{
		Path:   "test.txt",
		OldStr: "world",
		NewStr: "gopher",
	})
	if err != nil {
		t.Fatalf("EditTool failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("EditTool reported failure: %s", res.Error)
	}

	newContent, _ := os.ReadFile(filePath)
	if string(newContent) != "hello gopher\n" {
		t.Errorf("expected 'hello gopher\\n', got %q", string(newContent))
	}
}

func TestGitTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gitunstuck-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	WorkingDir = tempDir

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\noutput: %s", strings.Join(args, " "), err, out)
		}
	}
	runGit("init")

	ctx := &mockToolContext{Context: context.Background()}

	// Test allowed command
	res, err := GitTool(ctx, GitArgs{Args: []string{"status"}})
	if err != nil {
		t.Fatalf("GitTool failed: %v", err)
	}
	if !res.Ok {
		t.Errorf("expected Ok=true, got %v: %s", res.Ok, res.Output)
	}

	// Test blocked subcommand
	res, err = GitTool(ctx, GitArgs{Args: []string{"checkout", "main"}})
	if err != nil {
		t.Fatalf("GitTool failed: %v", err)
	}
	if res.Ok {
		t.Errorf("expected Ok=false for blocked subcommand, got true")
	}

	// Test blocked flag
	res, err = GitTool(ctx, GitArgs{Args: []string{"add", "--force", "."}})
	if err != nil {
		t.Fatalf("GitTool failed: %v", err)
	}
	if res.Ok {
		t.Errorf("expected Ok=false for blocked flag, got true")
	}
}
