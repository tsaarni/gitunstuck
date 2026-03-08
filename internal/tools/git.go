package tools

import (
	"fmt"
	"github.com/tsaarni/gitunstuck/internal/git"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

type GitArgs struct {
	Args []string `json:"args"`
}

type ThreeWayMergeArgs struct {
}

type ThreeWayMergeResult struct {
	Resolved   []string `json:"resolved" jsonschema:"Files successfully merged. Conflict markers removed."`
	Conflicted []string `json:"conflicted" jsonschema:"Files that still have conflict markers."`
}

func ThreeWayMergeTool(ctx tool.Context, args ThreeWayMergeArgs) (ThreeWayMergeResult, error) {
	client := &git.Client{BaseDir: WorkingDir}
	files, err := client.UnmergedFiles()
	if err != nil {
		return ThreeWayMergeResult{}, err
	}

	result := ThreeWayMergeResult{Resolved: []string{}, Conflicted: []string{}}
	for _, file := range files {
		if err := client.MergeFile(file); err == nil {
			result.Resolved = append(result.Resolved, file)
		} else {
			result.Conflicted = append(result.Conflicted, file)
		}
	}
	return result, nil
}

type GitResult struct {
	Ok     bool   `json:"ok" jsonschema:"True if git exited with code 0."`
	Output string `json:"output" jsonschema:"Output from the git command."`
}

var allowedSubcommands = map[string][]string{
	"Inspection": {"status", "diff", "log", "show", "grep", "blame", "ls-files", "rev-parse", "rev-list", "branch", "cat-file"},
	"Staging":    {"add", "rm"},
	"Resolve":    {"cherry-pick", "rebase", "restore"},
	"Utility":    {"stash"},
}

func getGitAllowlistDesc() string {
	var cats []string
	for cat, subs := range allowedSubcommands {
		cats = append(cats, fmt.Sprintf("[%s: %s]", cat, strings.Join(subs, ", ")))
	}
	sort.Strings(cats)
	return "Allowed subcommands: " + strings.Join(cats, ", ") + ". Blocked flags: --force, -f, --hard."
}

func GitTool(ctx tool.Context, args GitArgs) (GitResult, error) {
	if len(args.Args) == 0 {
		return GitResult{Output: "no arguments provided"}, nil
	}

	sub := args.Args[0]
	allowed := false
	for _, subs := range allowedSubcommands {
		for _, s := range subs {
			if s == sub {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return GitResult{Output: fmt.Sprintf("subcommand '%s' not allowed", sub)}, nil
	}

	for _, arg := range args.Args {
		if arg == "--force" || arg == "-f" || arg == "--hard" {
			return GitResult{Output: fmt.Sprintf("flag '%s' is blocked", arg)}, nil
		}
	}

	out, err := RunCommand("git", args.Args...)
	return GitResult{Ok: err == nil, Output: out}, nil
}

func GetMergeContextTool(ctx tool.Context, args struct{}) (*git.MergeInfo, error) {
	client := &git.Client{BaseDir: WorkingDir}
	return client.GetMergeContext()
}

func NewGitTool() tool.Tool {
	schema, _ := jsonschema.For[GitArgs](nil)
	t, _ := functiontool.New(functiontool.Config{Name: "git", InputSchema: schema}, GitTool)
	return t
}

func NewThreeWayMergeTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "run_3way_merge"}, ThreeWayMergeTool)
	return t
}

func NewGetMergeContextTool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{Name: "get_merge_context"}, GetMergeContextTool)
	return t
}
