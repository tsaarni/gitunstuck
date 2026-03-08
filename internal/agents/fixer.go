package agents

import (
	"bytes"
	"fmt"
	"github.com/tsaarni/gitunstuck/internal/git"
	"text/template"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool/exitlooptool"
	"google.golang.org/adk/util/instructionutil"
	"google.golang.org/genai"
)

const fixerTmpl = `# Role
Software Developer. Your goal is to resolve compilation errors and test regressions to ensure build integrity after integration.

# Workflow Pattern: Assess -> Plan -> Fix -> Verify -> Iterate

# Previous Attempts (If Any)
{fix_history?}

# Context
Conflicts from {{.Operation}} have been resolved for the following files:
{{range .ConflictedFiles}} - {{.}}
{{end}}
{{if .Base}}
Common Ancestor: {{.Base}}

Incoming vs Ancestor (git diff {{.Base}} {{.IncomingLabel}} --stat):
{{.IncomingDiff}}
Incoming logs (git log --oneline {{.Base}}..{{.IncomingLabel}}):
{{.IncomingLogs}}

Resolution vs Local HEAD (git diff HEAD --stat):
{{.WorktreeDiff}}
{{end}}

# Instructions
1. **Assess:** Use "run_build" to capture the initial error state.
2. **Plan (Per Failure):** Pick the first or most critical error and investigate its root cause.
   - Use "view" to see the failing line.
   - **Context Gathering:** Use "get_merge_context" to understand the original intent of the changes being integrated. Use "git show HEAD:<file>" or "git show ORIG_HEAD:<file>" to see how the code looked before it broke.
   - Example: "git show ORIG_HEAD:internal/utils/helper.go"
   - Use "git grep" to find all usages of a broken symbol to understand its expected signature.
3. **Fix & Stage:**
   - Apply the targeted fix using "edit" or "write".
   - Use "git add <file>" to stage the fix.
4. **Verify & Self-Correct:**
   - Run "run_build" again to verify the fix.
   - If the error persists or new ones emerge, analyze if your previous fix was incorrect or incomplete.
   - **Constraint:** Do not move to "run_tests" until "run_build" is green.
5. **Final Validation:**
   - Once the build passes, use "run_tests". Treat test failures with the same Assess-Plan-Fix-Verify workflow.
6. **Completion:** Use "exit_loop" ONLY when build and tests are green, or if you have reached a logical dead-end.

# Guidelines
- **Minimal Change:** Apply only the necessary changes to fix the build or test failure. Avoid unrelated refactorings or stylistic adjustments.
- **Atomic Fixes:** Fix errors incrementally. Do not apply unrelated refactorings or mass changes.
- **Root Cause Analysis:** Use git tools to find the commit that introduced the breaking change to understand the original developer's intent.
- **Regression Check:** If you fix a common utility or API signature, use "git grep" to ensure you've updated all call sites.
`

func NewFixer(cfg Config) (agent.Agent, error) {
	exitLoopTool, err := exitlooptool.New()
	if err != nil {
		return nil, err
	}

	agentCfg := llmagent.Config{
		Name:        "BuildFixer",
		Description: "Fixes compilation and test failures after merge conflict resolution.",
		Model:       cfg.Model,
		InstructionProvider: func(ctx agent.ReadonlyContext) (string, error) {
			tmpl, err := template.New("fixerPrompt").Parse(fixerTmpl)
			if err != nil {
				return "", fmt.Errorf("failed to parse fixer prompt: %w", err)
			}

			worktreeDiff, _ := cfg.GitClient.DiffHEADStat()

			data := struct {
				*git.MergeInfo
				WorktreeDiff string
			}{
				MergeInfo:    cfg.MergeContext,
				WorktreeDiff: worktreeDiff,
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return "", fmt.Errorf("failed to render fixer prompt: %w", err)
			}

			return instructionutil.InjectSessionState(ctx, buf.String())
		},
		Tools: append(BaseTools(), exitLoopTool),
		GenerateContentConfig: &genai.GenerateContentConfig{
			MaxOutputTokens: cfg.MaxOutputTokens,
		},
		IncludeContents:      llmagent.IncludeContentsNone,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{PruneHistory},
		AfterModelCallbacks:  []llmagent.AfterModelCallback{LogTokenUsage},
		AfterAgentCallbacks:  []agent.AfterAgentCallback{UpdateFixHistory},
	}
	return llmagent.New(agentCfg)
}
