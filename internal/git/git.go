package git

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	BaseDir string
}

func NewClient(baseDir string) *Client {
	return &Client{BaseDir: baseDir}
}

func (c *Client) exec(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	if c.BaseDir != "" {
		cmd.Dir = c.BaseDir
	}
	return cmd
}

func (c *Client) Git(args ...string) (string, error) {
	for _, arg := range args {
		if strings.Contains(arg, "..") {
			return "", fmt.Errorf("argument contains '..'")
		}
	}
	cmd := c.exec("git", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (c *Client) UnmergedFiles() ([]string, error) {
	out, err := c.exec("git", "diff", "--name-only", "--diff-filter=U").Output()
	if err != nil {
		return nil, err
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if file := scanner.Text(); file != "" {
			files = append(files, file)
		}
	}
	return files, nil
}

func (c *Client) MergeFile(path string) error {
	base, err := c.exec("git", "show", ":1:"+path).Output()
	if err != nil {
		return err
	}
	local, err := c.exec("git", "show", ":2:"+path).Output()
	if err != nil {
		return err
	}
	incoming, err := c.exec("git", "show", ":3:"+path).Output()
	if err != nil {
		return err
	}

	tempBase := path + ".base"
	tempLocal := path + ".local"
	tempIncoming := path + ".incoming"

	if c.BaseDir != "" {
		tempBase = filepath.Join(c.BaseDir, tempBase)
		tempLocal = filepath.Join(c.BaseDir, tempLocal)
		tempIncoming = filepath.Join(c.BaseDir, tempIncoming)
	}

	os.WriteFile(tempBase, base, 0644)
	os.WriteFile(tempLocal, local, 0644)
	os.WriteFile(tempIncoming, incoming, 0644)
	defer os.Remove(tempBase)
	defer os.Remove(tempLocal)
	defer os.Remove(tempIncoming)

	// git merge-file <local> <base> <incoming>
	cmd := c.exec("git", "merge-file", tempLocal, tempBase, tempIncoming)
	err = cmd.Run()

	if err == nil {
		mergedContent, err := os.ReadFile(tempLocal)
		if err != nil {
			return err
		}

		targetPath := path
		if c.BaseDir != "" {
			targetPath = filepath.Join(c.BaseDir, path)
		}
		return os.WriteFile(targetPath, mergedContent, 0644)
	}

	return err
}

func (c *Client) Add(path string) error {
	cmd := c.exec("git", "add", path)
	return cmd.Run()
}

type MergeInfo struct {
	Type          string   `json:"type" jsonschema:"Type of operation (merge, rebase, cherry-pick)"`
	Files         []string `json:"files" jsonschema:"Unmerged files"`
	Local         []string `json:"local" jsonschema:"Local branch commits"`
	Incoming      []string `json:"incoming" jsonschema:"Incoming branch commits"`
	Base          string   `json:"base"`
	LocalDiff     string   `json:"local_diff"`
	IncomingDiff  string   `json:"incoming_diff"`
	IncomingLabel string   `json:"incoming_label"`
}

func (c *Client) DiffHEADStat() (string, error) {
	return c.Git("diff", "HEAD", "--stat")
}

func (c *Client) ValidateArgs(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no arguments provided")
	}

	sub := args[0]
	allowed := false
	allowedSubcommands := map[string][]string{
		"Inspection": {"status", "diff", "log", "show", "grep", "blame", "ls-files", "rev-parse", "rev-list", "branch", "cat-file"},
		"Staging":    {"add", "rm"},
		"Resolve":    {"rebase", "cherry-pick", "restore"},
		"Utility":    {"stash"},
	}

	for _, subs := range allowedSubcommands {
		for _, s := range subs {
			if s == sub {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return fmt.Errorf("subcommand '%s' not allowed", sub)
	}

	for _, arg := range args {
		if arg == "--force" || arg == "-f" || arg == "--hard" || arg == "--pager" {
			return fmt.Errorf("flag '%s' is blocked", arg)
		}
	}

	if sub == "rebase" {
		allowedFlags := []string{"--continue", "--abort", "--skip"}
		isAllowedFlag := false
		for _, arg := range args[1:] {
			for _, allowedFlag := range allowedFlags {
				if arg == allowedFlag {
					isAllowedFlag = true
					break
				}
			}
		}
		if !isAllowedFlag {
			return fmt.Errorf("subcommand 'rebase' is only allowed with --continue, --abort, or --skip")
		}
	}
	return nil
}

func (c *Client) GetMergeContext() (*MergeInfo, error) {
	files, err := c.UnmergedFiles()
	if err != nil {
		return nil, err
	}

	info := &MergeInfo{Files: files}

	// Detect merge/rebase/cherry-pick
	if _, err := os.Stat(filepath.Join(c.BaseDir, ".git/MERGE_HEAD")); err == nil {
		info.Type = "merge"
		info.IncomingLabel = "MERGE_HEAD"
		info.Base, _ = c.Git("merge-base", "HEAD", "MERGE_HEAD")
		info.Base = strings.TrimSpace(info.Base)
		info.LocalDiff, _ = c.Git("diff", info.Base, "HEAD", "--stat")
		info.IncomingDiff, _ = c.Git("diff", info.Base, "MERGE_HEAD", "--stat")
		info.Local, _ = c.log("HEAD", "MERGE_HEAD")
		info.Incoming, _ = c.log("MERGE_HEAD", "HEAD")
	} else if _, err := os.Stat(filepath.Join(c.BaseDir, ".git/rebase-apply")); err == nil {
		info.Type = "rebase"
		info.IncomingLabel = "rebase"
	} else if _, err := os.Stat(filepath.Join(c.BaseDir, ".git/rebase-merge")); err == nil {
		info.Type = "rebase"
		info.IncomingLabel = "rebase"
	} else if _, err := os.Stat(filepath.Join(c.BaseDir, ".git/CHERRY_PICK_HEAD")); err == nil {
		info.Type = "cherry-pick"
		info.IncomingLabel = "CHERRY_PICK_HEAD"
	}

	return info, nil
}

func (c *Client) log(branch, other string) ([]string, error) {
	out, err := c.exec("git", "log", "--oneline", "-n", "10", branch+".."+other).Output()
	if err != nil {
		return nil, err
	}
	var logs []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		logs = append(logs, scanner.Text())
	}
	return logs, nil
}
