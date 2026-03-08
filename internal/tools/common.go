package tools

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorkingDir is the global base path for all tool operations.
// It must be set during initialization.
var WorkingDir string

// SecurePath resolves a path relative to the global WorkingDir and ensures no escapes.
func SecurePath(inputPath string) (string, error) {
	if WorkingDir == "" {
		return "", fmt.Errorf("tools.WorkingDir is not set")
	}
	absBase, _ := filepath.Abs(WorkingDir)

	fullPath := inputPath
	if !filepath.IsAbs(inputPath) {
		fullPath = filepath.Join(absBase, inputPath)
	}

	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(absPath, absBase) {
		return "", fmt.Errorf("path escapes working directory: %s", inputPath)
	}
	return absPath, nil
}

// ReadLines reads lines from a file within the global WorkingDir.
func ReadLines(path string, startLine, limit int) ([]string, error) {
	absPath, err := SecurePath(path)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	currentLine := 0
	for scanner.Scan() {
		currentLine++
		if currentLine < startLine {
			continue
		}
		if limit >= 0 && len(lines) >= limit {
			break
		}
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// WriteFile writes content to a file within the global WorkingDir.
func WriteFile(path, content string, createParent bool) error {
	absPath, err := SecurePath(path)
	if err != nil {
		return err
	}

	if createParent {
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(absPath, []byte(content), 0644)
}

// RunCommand runs a command strictly within the global WorkingDir.
func RunCommand(name string, args ...string) (string, error) {
	for _, arg := range args {
		if strings.Contains(arg, "..") {
			return "", fmt.Errorf("argument contains '..': %s", arg)
		}
		if filepath.IsAbs(arg) {
			if _, err := SecurePath(arg); err != nil {
				return "", err
			}
		}
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = WorkingDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	return out.String(), err
}
