package tools

import (
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

var BuildCommand string
var TestCommand string

func BuildProjectTool(ctx tool.Context, args struct{}) (GitResult, error) {
	out, err := RunCommand("sh", "-c", BuildCommand)
	return GitResult{Ok: err == nil, Output: out}, nil
}

func TestProjectTool(ctx tool.Context, args struct{}) (GitResult, error) {
	out, err := RunCommand("sh", "-c", TestCommand)
	return GitResult{Ok: err == nil, Output: out}, nil
}

func NewProjectBuildTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "project_build",
		Description: "Build the project using the configured build command.",
	}, BuildProjectTool)
	return t
}

func NewProjectTestTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "project_test",
		Description: "Run tests for the project using the configured test command.",
	}, TestProjectTool)
	return t
}
