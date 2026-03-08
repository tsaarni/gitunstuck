package tools

import (
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type ViewArgs struct {
	Path      string `json:"path" jsonschema:"Path to file or directory."`
	ViewRange []int  `json:"view_range,omitempty" jsonschema:"[start, end]"`
}

type ViewResult struct {
	Content string `json:"content"`
}

func ViewTool(ctx tool.Context, args ViewArgs) (ViewResult, error) {
	absPath, err := SecurePath(args.Path)
	if err != nil {
		return ViewResult{}, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return ViewResult{}, err
	}

	if info.IsDir() {
		files, _ := os.ReadDir(absPath)
		var b strings.Builder
		for _, f := range files {
			b.WriteString(f.Name() + "\n")
		}
		return ViewResult{Content: b.String()}, nil
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
		return ViewResult{}, err
	}

	var b strings.Builder
	for i, line := range lines {
		b.WriteString(fmt.Sprintf("%d. %s\n", start+i, line))
	}
	return ViewResult{Content: b.String()}, nil
}

type EditArgs struct {
	Path   string `json:"path"`
	OldStr string `json:"old_str"`
	NewStr string `json:"new_str"`
}

type SuccessResult struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func EditTool(ctx tool.Context, args EditArgs) (SuccessResult, error) {
	absPath, err := SecurePath(args.Path)
	if err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}

	newContent := strings.Replace(string(content), args.OldStr, args.NewStr, 1)
	if strings.Count(string(content), args.OldStr) != 1 {
		return SuccessResult{Error: "old_str not found or ambiguous"}, nil
	}

	if err := WriteFile(args.Path, newContent, false); err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}
	return SuccessResult{Success: true}, nil
}

type WriteArgs struct {
	Path     string `json:"path"`
	FileText string `json:"file_text"`
}

func WriteTool(ctx tool.Context, args WriteArgs) (SuccessResult, error) {
	if err := WriteFile(args.Path, args.FileText, false); err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}
	return SuccessResult{Success: true}, nil
}

type CreateArgs struct {
	Path     string `json:"path"`
	FileText string `json:"file_text"`
}

func CreateTool(ctx tool.Context, args CreateArgs) (SuccessResult, error) {
	if err := WriteFile(args.Path, args.FileText, true); err != nil {
		return SuccessResult{Error: err.Error()}, nil
	}
	return SuccessResult{Success: true}, nil
}

func NewViewTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "view"}, ViewTool)
	return t
}
func NewEditTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "edit"}, EditTool)
	return t
}
func NewWriteTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "write"}, WriteTool)
	return t
}
func NewCreateTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "create"}, CreateTool)
	return t
}
