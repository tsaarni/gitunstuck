package agents

import (
	"bytes"
	"fmt"
	"github.com/tsaarni/gitunstuck/internal/tools"
	"text/template"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

const resolverTmpl = `# Role
Software Developer. Your goal is to resolve merge conflicts at the source level while preserving semantic intent and code integrity.

# Workflow Pattern: Assess -> Plan -> Resolve -> Audit -> Verify -> Reflect

# Context
Please resolve the {{.Operation}} conflicts in the following files:
{{range .ConflictedFiles}} - {{.}}
{{end}}
{{if .Base}}
Common Ancestor: {{.Base}}

Local vs Ancestor (git diff {{.Base}} HEAD --stat):
{{.LocalDiff}}
Local logs (git log --oneline {{.Base}}..HEAD):
{{.LocalLogs}}
Incoming vs Ancestor (git diff {{.Base}} {{.IncomingLabel}} --stat):
{{.IncomingDiff}}
Incoming logs (git log --oneline {{.Base}}..{{.IncomingLabel}}):
{{.IncomingLogs}}
{{end}}

# Instructions
1. **Assess:** Use "git status" to list all unmerged files.
2. **Deterministic 3-Way Merge (Bulk):**
   - Call "run_3way_merge" once to automatically resolve files.
   - Use the returned lists ("resolved" and "conflicted") to understand your starting point.
   - **Crucial:** You MUST still audit files in the "resolved" list in step 4.
3. **Manual Resolution:**
   - Focus on files from the "conflicted" list.
   - **Observe:** Use "get_merge_context" and "view" to identify conflict segments.
   - Use "git show HEAD:<file>" (OURS) and "git show MERGE_HEAD:<file>" (THEIRS) to understand the delta.
   - Resolve by merging the logical intent using "edit" or "write".
   - **Constraint:** Do not leave any markers (<<<<<<<, =======, >>>>>>>) in the final output.
4. **Audit (Mandatory):**
   - Review ALL resolved files (both from deterministic merge and manual fixes).
   - Use "view" to read the resolved sections and ensure semantic correctness.
5. **Verify:**
   - Use "git grep '<<<<<<<'" to verify that NO conflict markers remain in any file.
   - Use "git add <file>" only after successful audit and verification.
6. **Reflect:**
   - Before completing, ask: "Does this resolution introduce any obvious syntax errors or break existing imports seen in 'git show'?"

# Guidelines
- **Minimal Change:** Apply only the necessary changes to resolve the conflict. Avoid unrelated refactorings or stylistic adjustments.
- **Intent Preservation:** Prioritize semantic correctness over "picking a side."
- **Contextual Awareness:** Use "git grep" to see if your resolution affects other files (e.g., if a function signature was changed in one branch).
- **Failure Protocol:** If a conflict is logically unsolvable (e.g., mutual exclusion of critical logic), stop and report the contradiction clearly.
`

func NewResolver(cfg Config) (agent.Agent, error) {
	agentCfg := llmagent.Config{
		Name:        "ConflictResolver",
		Description: "Resolves Git merge conflicts using intent-aware analysis.",
		Model:       cfg.Model,
		InstructionProvider: func(ctx agent.ReadonlyContext) (string, error) {
			tmpl, err := template.New("resolverPrompt").Parse(resolverTmpl)
			if err != nil {
				return "", fmt.Errorf("failed to parse resolver prompt: %w", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, cfg.MergeContext); err != nil {
				return "", fmt.Errorf("failed to render resolver prompt: %w", err)
			}

			return buf.String(), nil
		},
		Tools: append([]tool.Tool{tools.NewThreeWayMergeTool()}, BaseTools()...),
		GenerateContentConfig: &genai.GenerateContentConfig{
			MaxOutputTokens: cfg.MaxOutputTokens,
		},
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{PruneHistory},
		AfterModelCallbacks:  []llmagent.AfterModelCallback{LogTokenUsage},
	}
	return llmagent.New(agentCfg)
}
