package cli

import (
	"fmt"
	"strings"
)

// maxDiffLines bounds how much of a diff a plan prints.
const maxDiffLines = 40

// lineDiff is a minimal line diff of a and b: "  " kept, "- " removed,
// "+ " added. Plans only carry short text, so the quadratic
// longest-common-subsequence table is fine.
func lineDiff(a, b string) []string {
	x, y := lines(a), lines(b)
	t := lcs(x, y)
	var out []string
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			out = append(out, "  "+x[i])
			i, j = i+1, j+1
		case i < len(x) && (j == len(y) || t[i+1][j] >= t[i][j+1]):
			out = append(out, "- "+x[i])
			i++
		default:
			out = append(out, "+ "+y[j])
			j++
		}
	}
	if len(out) > maxDiffLines {
		out = append(out[:maxDiffLines], fmt.Sprintf("... %d more lines", len(out)-maxDiffLines))
	}
	return out
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// lcs[i][j] is the longest common subsequence length of x[i:] and y[j:].
func lcs(x, y []string) [][]int {
	t := make([][]int, len(x)+1)
	for i := range t {
		t[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			t[i][j] = cell(t, x, y, i, j)
		}
	}
	return t
}

func cell(t [][]int, x, y []string, i, j int) int {
	if x[i] == y[j] {
		return t[i+1][j+1] + 1
	}
	return max(t[i+1][j], t[i][j+1])
}
