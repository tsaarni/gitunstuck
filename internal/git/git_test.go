package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestRepo(t *testing.T) string {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "gitunstuck-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\noutput: %s", strings.Join(args, " "), err, out)
		}
	}

	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")

	// Create a file and commit it
	filename := "test.txt"
	filepath := filepath.Join(tempDir, filename)
	if err := os.WriteFile(filepath, []byte("base content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "-m", "initial commit")

	// Create a branch and modify the file
	runGit("checkout", "-b", "feature")
	if err := os.WriteFile(filepath, []byte("feature content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "-m", "feature commit")

	// Back to main and modify the same file differently
	runGit("checkout", "main")
	if err := os.WriteFile(filepath, []byte("main content\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "-m", "main commit")

	// Merge feature into main to create a conflict
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = tempDir
	_ = cmd.Run() // Expect failure due to conflict

	return tempDir
}

func TestUnmergedFiles(t *testing.T) {
	tempDir := setupTestRepo(t)
	defer os.RemoveAll(tempDir)

	client := &Client{BaseDir: tempDir}
	files, err := client.UnmergedFiles()
	if err != nil {
		t.Fatalf("UnmergedFiles failed: %v", err)
	}

	if len(files) != 1 || files[0] != "test.txt" {
		t.Errorf("expected [test.txt], got %v", files)
	}
}

func TestGetMergeContext(t *testing.T) {
	tempDir := setupTestRepo(t)
	defer os.RemoveAll(tempDir)

	client := &Client{BaseDir: tempDir}
	info, err := client.GetMergeContext()
	if err != nil {
		t.Fatalf("GetMergeContext failed: %v", err)
	}

	if info.Base == "" {
		t.Errorf("info.Base is empty")
	}
	if info.LocalDiff == "" {
		t.Errorf("info.LocalDiff is empty")
	}
	if info.IncomingDiff == "" {
		t.Errorf("info.IncomingDiff is empty")
	}

	diffHeadStat, err := client.DiffHEADStat()
	if err != nil {
		t.Fatalf("DiffHEADStat failed: %v", err)
	}
	if diffHeadStat == "" {
		t.Errorf("DiffHEADStat is empty")
	}
}

func TestMergeFile(t *testing.T) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "gitunstuck-test-mergefile-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tempDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\noutput: %s", strings.Join(args, " "), err, out)
		}
	}

	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")

	// Create a multiline file
	filename := "test.txt"
	filepath := filepath.Join(tempDir, filename)
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(filepath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "-m", "initial commit")

	// Create a branch and modify top part
	runGit("checkout", "-b", "feature")
	newContent := "feature change\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(filepath, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "-m", "feature commit")

	// Back to main and modify bottom part
	runGit("checkout", "main")
	newContent = "line 1\nline 2\nline 3\nline 4\nmain change\n"
	if err := os.WriteFile(filepath, []byte(newContent), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "-m", "main commit")

	// Merge feature into main
	cmd := exec.Command("git", "merge", "feature")
	cmd.Dir = tempDir
	_ = cmd.Run()

	// Redo with overlapping changes to force conflict
	runGit("checkout", "feature")
	if err := os.WriteFile(filepath, []byte("feature change overlapping\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "--amend", "--no-edit")

	runGit("checkout", "main")
	if err := os.WriteFile(filepath, []byte("main change overlapping\n"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	runGit("add", filename)
	runGit("commit", "--amend", "--no-edit")

	cmd = exec.Command("git", "merge", "feature")
	cmd.Dir = tempDir
	_ = cmd.Run() // Definitely conflict now

	client := &Client{BaseDir: tempDir}
	err = client.MergeFile(filename)
	if err == nil {
		t.Errorf("expected MergeFile to return error for overlapping changes, got nil")
	}
}
