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

func TestFileReadTool(t *testing.T) {
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
	res, err := FileReadTool(ctx, FileReadArgs{Path: "test.txt"})
	if err != nil {
		t.Fatalf("FileReadTool failed: %v", err)
	}
	if !strings.Contains(res.Content, "1. line 1") {
		t.Errorf("expected line 1, got %s", res.Content)
	}

	// Test viewing a directory
	res, err = FileReadTool(ctx, FileReadArgs{Path: "."})
	if err != nil {
		t.Fatalf("FileReadTool failed: %v", err)
	}
	if !strings.Contains(res.Content, "test.txt") {
		t.Errorf("expected test.txt in listing, got %s", res.Content)
	}
}

func TestFileEditRegexTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gitunstuck-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	WorkingDir = tempDir

	// Create a sub-directory and some files
	subDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	file1 := filepath.Join(subDir, "file1.txt")
	if err := os.WriteFile(file1, []byte("hello world\n"), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}

	file2 := filepath.Join(subDir, "file2.txt")
	if err := os.WriteFile(file2, []byte("nothing here\n"), 0644); err != nil {
		t.Fatalf("failed to write file2: %v", err)
	}

	ctx := &mockToolContext{Context: context.Background()}

	// Test glob match and literal-like regex replacement
	res, err := FileEditRegexTool(ctx, FileEditRegexArgs{
		Path:   "src/*.txt",
		OldStr: "world",
		NewStr: "gopher",
	})
	if err != nil {
		t.Fatalf("FileEditRegexTool failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("FileEditRegexTool reported failure: %s", res.Error)
	}
	if res.TotalMatches != 1 {
		t.Errorf("expected 1 total match, got %d", res.TotalMatches)
	}

	newContent, _ := os.ReadFile(file1)
	if string(newContent) != "hello gopher\n" {
		t.Errorf("expected 'hello gopher\\n', got %q", string(newContent))
	}

	// Test multiple matches across different files (should now succeed)
	if err := os.WriteFile(file1, []byte("123 abc\n"), 0644); err != nil {
		t.Fatalf("failed to reset file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("456 def\n"), 0644); err != nil {
		t.Fatalf("failed to reset file2: %v", err)
	}

	res, err = FileEditRegexTool(ctx, FileEditRegexArgs{
		Path:   "src/*.txt",
		OldStr: `[0-9]+`,
		NewStr: "digits",
	})
	if err != nil {
		t.Fatalf("FileEditRegexTool multi-file check failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected FileEditRegexTool to succeed for multiple matches across files, got error: %s", res.Error)
	}
	if res.TotalMatches != 2 {
		t.Errorf("expected 2 total matches, got %d", res.TotalMatches)
	}
	if len(res.FilesUpdated) != 2 {
		t.Errorf("expected 2 files updated, got %d", len(res.FilesUpdated))
	}

	content1, _ := os.ReadFile(file1)
	if string(content1) != "digits abc\n" {
		t.Errorf("expected 'digits abc\\n' in file1, got %q", string(content1))
	}
	content2, _ := os.ReadFile(file2)
	if string(content2) != "digits def\n" {
		t.Errorf("expected 'digits def\\n' in file2, got %q", string(content2))
	}
}

func TestFileEditLinesTool(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gitunstuck-tools-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	WorkingDir = tempDir

	filePath := filepath.Join(tempDir, "test.txt")
	content := "line 1\nline 2\nline 3\nline 4\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ctx := &mockToolContext{Context: context.Background()}

	// Replace lines 2 and 3
	res, err := FileEditLinesTool(ctx, FileEditLinesArgs{
		Path:      "test.txt",
		StartLine: 2,
		EndLine:   3,
		Text:      "new line 2\nnew line 3",
	})
	if err != nil {
		t.Fatalf("FileEditLinesTool failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("FileEditLinesTool reported failure: %s", res.Error)
	}

	newContent, _ := os.ReadFile(filePath)
	expected := "line 1\nnew line 2\nnew line 3\nline 4\n"
	if string(newContent) != expected {
		t.Errorf("expected %q, got %q", expected, string(newContent))
	}

	// Test invalid range
	res, err = FileEditLinesTool(ctx, FileEditLinesArgs{
		Path:      "test.txt",
		StartLine: 5,
		EndLine:   4,
		Text:      "fail",
	})
	if err != nil {
		t.Fatalf("FileEditLinesTool failed: %v", err)
	}
	if res.Success {
		t.Errorf("expected failure for invalid range")
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

	// Test blocked command (apply)
	res, err = GitTool(ctx, GitArgs{Args: []string{"apply", "--help"}})
	if err != nil {
		t.Fatalf("GitTool failed: %v", err)
	}
	if !strings.Contains(res.Output, "subcommand 'apply' not allowed") {
		t.Errorf("expected 'apply' to be blocked, but got: %s", res.Output)
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
