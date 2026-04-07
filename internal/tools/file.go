package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type FileReadArgs struct {
	Path      string `json:"path" jsonschema:"File or directory path."`
	ViewRange []int  `json:"view_range,omitempty" jsonschema:"[start, end]"`
}

type FileReadResult struct {
	Content string `json:"content"`
}

func FileReadTool(ctx tool.Context, args FileReadArgs) (FileReadResult, error) {
	absPath, err := SecurePath(args.Path)
	if err != nil {
		return FileReadResult{}, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return FileReadResult{}, err
	}

	if info.IsDir() {
		files, _ := os.ReadDir(absPath)
		var b strings.Builder
		for _, f := range files {
			b.WriteString(f.Name() + "\n")
		}
		return FileReadResult{Content: b.String()}, nil
	}

	start, limit := 1, -1
	if len(args.ViewRange) == 2 {
		start = args.ViewRange[0]
		if end := args.ViewRange[1]; end != -1 {
			limit = end - start + 1
		}
	}

	lines, err := ReadLines(args.Path, start, limit)
	if err != nil {
		return FileReadResult{}, err
	}

	var b strings.Builder
	for i, line := range lines {
		b.WriteString(fmt.Sprintf("%d. %s\n", start+i, line))
	}
	return FileReadResult{Content: b.String()}, nil
}

type FileEditRegexArgs struct {
	Path   string `json:"path" jsonschema:"Glob pattern for files to search in."`
	OldStr string `json:"old_str" jsonschema:"Regular expression to find."`
	NewStr string `json:"new_str" jsonschema:"Replacement string."`
}

type FileEditRegexResult struct {
	Success      bool   `json:"success"`
	FilesUpdated []string `json:"files_updated,omitempty" jsonschema:"Files where replacements were made."`
	TotalMatches int    `json:"total_matches" jsonschema:"Total replacements made across all files."`
	Error        string `json:"error,omitempty"`
}

func FileEditRegexTool(ctx tool.Context, args FileEditRegexArgs) (FileEditRegexResult, error) {
	pattern := filepath.Join(WorkingDir, args.Path)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return FileEditRegexResult{Error: fmt.Sprintf("invalid glob pattern: %v", err)}, nil
	}

	if len(matches) == 0 {
		return FileEditRegexResult{Error: "no files matched the glob pattern"}, nil
	}

	re, err := regexp.Compile(args.OldStr)
	if err != nil {
		return FileEditRegexResult{Error: fmt.Sprintf("invalid regex: %v", err)}, nil
	}

	result := FileEditRegexResult{Success: true, FilesUpdated: []string{}}
	totalMatches := 0

	for _, match := range matches {
		rel, err := filepath.Rel(WorkingDir, match)
		if err != nil {
			continue
		}
		if _, err := SecurePath(rel); err != nil {
			continue
		}

		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}

		content, err := os.ReadFile(match)
		if err != nil {
			continue
		}

		fileMatches := re.FindAllIndex(content, -1)
		if len(fileMatches) > 0 {
			totalMatches += len(fileMatches)
			newContent := re.ReplaceAllString(string(content), args.NewStr)
			if err := WriteFile(rel, newContent, false); err != nil {
				return FileEditRegexResult{Error: fmt.Sprintf("failed to write %s: %v", rel, err)}, nil
			}
			result.FilesUpdated = append(result.FilesUpdated, rel)
		}
	}

	result.TotalMatches = totalMatches
	if totalMatches == 0 {
		result.Success = false
		result.Error = "old_str not found in any of the matched files"
	}

	return result, nil
}

type FileEditLinesArgs struct {
	Path      string `json:"path" jsonschema:"Path to the file."`
	StartLine int    `json:"start_line" jsonschema:"1-based start line number."`
	EndLine   int    `json:"end_line" jsonschema:"1-based end line number (inclusive)."`
	Text      string `json:"text" jsonschema:"New block of text."`
}

func FileEditLinesTool(ctx tool.Context, args FileEditLinesArgs) (SuccessResult, error) {
	if args.StartLine < 1 || args.EndLine < args.StartLine {
		return SuccessResult{Error: "invalid line range"}, nil
	}

	allLines, err := ReadLines(args.Path, 1, -1)
	if err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}

	if args.StartLine > len(allLines) {
		return SuccessResult{Error: "start_line beyond file end"}, nil
	}

	end := args.EndLine
	if end > len(allLines) {
		end = len(allLines)
	}

	var newLines []string
	newLines = append(newLines, allLines[:args.StartLine-1]...)
	newLines = append(newLines, args.Text)
	newLines = append(newLines, allLines[end:]...)

	newContent := strings.Join(newLines, "\n")
	if !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}

	if err := WriteFile(args.Path, newContent, false); err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}

	return SuccessResult{Success: true}, nil
}

type FileWriteArgs struct {
	Path     string `json:"path" jsonschema:"Path to an existing file."`
	FileText string `json:"file_text" jsonschema:"Complete new content."`
}

type FileCreateArgs struct {
	Path     string `json:"path" jsonschema:"Path for the new file."`
	FileText string `json:"file_text" jsonschema:"Full content for the new file."`
}

type SuccessResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func FileWriteTool(ctx tool.Context, args FileWriteArgs) (SuccessResult, error) {
	if err := WriteFile(args.Path, args.FileText, false); err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}
	return SuccessResult{Success: true}, nil
}

func FileCreateTool(ctx tool.Context, args FileCreateArgs) (SuccessResult, error) {
	if err := WriteFile(args.Path, args.FileText, true); err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}
	return SuccessResult{Success: true}, nil
}

func NewFileReadTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "file_read",
		Description: "Read file contents with line numbers, or list a directory. All paths are relative to the repository root.",
	}, FileReadTool)
	return t
}
func NewFileEditRegexTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "file_edit_regex",
		Description: "Search for a regex (Go RE2 syntax) across files matching a glob and replace all occurrences. Supports match groups ($1, $2, etc.) in replacement. All paths are relative to the repository root.",
	}, FileEditRegexTool)
	return t
}
func NewFileEditLinesTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "file_edit_lines",
		Description: "Replace a range of lines in a file with a new block of text. All paths are relative to the repository root.",
	}, FileEditLinesTool)
	return t
}
func NewFileWriteTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "file_write",
		Description: "Overwrite an existing file with new content. All paths are relative to the repository root.",
	}, FileWriteTool)
	return t
}
func NewFileCreateTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "file_create",
		Description: "Create a new file with the specified content. Parent directories are created automatically. All paths are relative to the repository root.",
	}, FileCreateTool)
	return t
}
