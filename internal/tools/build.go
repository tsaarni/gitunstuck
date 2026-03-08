package tools

import (
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

var (
	BuildCommand string
	TestCommand  string
)

type CommandResult struct {
	Ok     bool   `json:"ok" jsonschema:"True if the command exited with code 0."`
	Output string `json:"output" jsonschema:"Combined stdout and stderr from the command."`
}

func RunBuildTool(ctx tool.Context, args struct{}) (CommandResult, error) {
	out, err := RunCommand("sh", "-c", BuildCommand)
	return CommandResult{Ok: err == nil, Output: out}, nil
}

func RunTestsTool(ctx tool.Context, args struct{}) (CommandResult, error) {
	out, err := RunCommand("sh", "-c", TestCommand)
	return CommandResult{Ok: err == nil, Output: out}, nil
}

func NewRunBuildTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "run_build"}, RunBuildTool)
	return t
}

func NewRunTestsTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "run_tests"}, RunTestsTool)
	return t
}
