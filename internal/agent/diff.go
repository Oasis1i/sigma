package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolDiff is the unified diff a UI should show for a tool call.
// edit_file diffs the old and new snippets the model sent. That is the change
// it asked for, which is useful even when the edit is rejected for matching
// more than once. Other tools have no diff.
func ToolDiff(name, arguments string) string {
	if name != "edit_file" {
		return ""
	}
	var args struct {
		Path string `json:"path"`
		Old  string `json:"old_string"`
		New  string `json:"new_string"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	if args.Old == "" && args.New == "" {
		return ""
	}
	path := args.Path
	if path == "" {
		path = "file"
	}
	return snippetDiff(path, args.Old, args.New)
}

// snippetDiff is a small line diff of the replaced region, not of the whole
// file. The loop does not need to re-read the file to explain the edit, and
// a full-file diff would bury a one-line change in a large source file.
// A huge region falls back to "everything removed, everything added" so a
// pathological edit cannot spend quadratic time in the UI path.
func snippetDiff(path, oldText, newText string) string {
	oldLines := splitDiffLines(oldText)
	newLines := splitDiffLines(newText)
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@\n", path, path)
	if len(oldLines)*len(newLines) > 4000 {
		writeRemovedAdded(&b, oldLines, newLines)
		return b.String()
	}
	writeLCS(&b, oldLines, newLines)
	return b.String()
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") && len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func writeRemovedAdded(b *strings.Builder, oldLines, newLines []string) {
	for _, line := range oldLines {
		fmt.Fprintf(b, "-%s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(b, "+%s\n", line)
	}
}

// writeLCS prints a shortest edit script from the longest common subsequence.
// Context lines keep a leading space, the usual unified-diff convention.
func writeLCS(b *strings.Builder, oldLines, newLines []string) {
	n, m := len(oldLines), len(newLines)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		if oldLines[i] == newLines[j] {
			fmt.Fprintf(b, " %s\n", oldLines[i])
			i++
			j++
			continue
		}
		// Prefer deleting when both choices keep the same amount of LCS.
		// That puts the "-" line before the "+" line, which reads like a diff.
		if dp[i+1][j] >= dp[i][j+1] {
			fmt.Fprintf(b, "-%s\n", oldLines[i])
			i++
		} else {
			fmt.Fprintf(b, "+%s\n", newLines[j])
			j++
		}
	}
	for ; i < n; i++ {
		fmt.Fprintf(b, "-%s\n", oldLines[i])
	}
	for ; j < m; j++ {
		fmt.Fprintf(b, "+%s\n", newLines[j])
	}
}
