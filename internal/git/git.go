package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	BaseDir string
}

func (c *Client) exec(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	if c.BaseDir != "" {
		cmd.Dir = c.BaseDir
	}
	return cmd
}

func (c *Client) StatusPorcelain() ([]string, error) {
	cmd := c.exec("git", "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

func (c *Client) UnmergedFiles() ([]string, error) {
	lines, err := c.StatusPorcelain()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		status := line[:2]
		if strings.Contains(status, "U") || status == "AA" || status == "DD" {
			files = append(files, strings.TrimSpace(line[3:]))
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
	Base            string
	LocalLabel      string
	IncomingLabel   string
	LocalDiff       string
	IncomingDiff    string
	LocalLogs       string
	IncomingLogs    string
	ConflictedFiles []string
	Operation       string
}

func (c *Client) DiffHEADStat() (string, error) {
	out, err := c.exec("git", "diff", "HEAD", "--stat").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (c *Client) GetMergeContext() (*MergeInfo, error) {
	var info MergeInfo
	var other string

	mergeHead, err := c.exec("git", "rev-parse", "MERGE_HEAD").Output()
	if err == nil {
		// Standard Merge
		other = strings.TrimSpace(string(mergeHead))
		info.IncomingLabel = "MERGE_HEAD"
		info.LocalLabel = "HEAD"
		info.Operation = "merge"
	} else {
		// Check for Rebase
		rebaseHead, err := c.exec("git", "rev-parse", "REBASE_HEAD").Output()
		if err == nil {
			// Rebase in progress
			other = strings.TrimSpace(string(rebaseHead))
			info.IncomingLabel = "REBASE_HEAD"
			info.LocalLabel = "HEAD (rebase-onto)"
			info.Operation = "rebase"
		} else {
			return nil, fmt.Errorf("no active merge or rebase detected (MERGE_HEAD or REBASE_HEAD not found)")
		}
	}

	files, err := c.UnmergedFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to get unmerged files: %w", err)
	}
	info.ConflictedFiles = files

	base, err := c.exec("git", "merge-base", "HEAD", other).Output()
	if err != nil {
		return nil, fmt.Errorf("could not find common ancestor between HEAD and %s", info.IncomingLabel)
	}
	info.Base = strings.TrimSpace(string(base))

	diffLocal, _ := c.exec("git", "diff", info.Base, "HEAD", "--stat").Output()
	info.LocalDiff = string(diffLocal)

	diffIncoming, _ := c.exec("git", "diff", info.Base, other, "--stat").Output()
	info.IncomingDiff = string(diffIncoming)

	localLogs, _ := c.exec("git", "log", "--oneline", fmt.Sprintf("%s..HEAD", info.Base)).Output()
	info.LocalLogs = string(localLogs)

	logs, _ := c.exec("git", "log", "--oneline", fmt.Sprintf("%s..%s", info.Base, other)).Output()
	info.IncomingLogs = string(logs)

	return &info, nil
}
