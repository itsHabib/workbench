package plan

import "strings"

const (
	maxDiffLines = 200
	diffContext  = 2
)

// lineDiff renders a small LCS line diff: "- " removed, "+ " added,
// "  " context, with unchanged runs beyond diffContext elided.
func lineDiff(a, b string) []string {
	x, y := splitLines(a), splitLines(b)
	if len(x) > maxDiffLines || len(y) > maxDiffLines {
		return []string{"(content too large to diff)"}
	}
	lcs := lcsTable(x, y)
	var ops []string
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, "  "+x[i])
			i++
			j++
		case j == len(y) || (i < len(x) && lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, "- "+x[i])
			i++
		default:
			ops = append(ops, "+ "+y[j])
			j++
		}
	}
	return elide(ops)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func lcsTable(x, y []string) [][]int {
	t := make([][]int, len(x)+1)
	for i := range t {
		t[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		fillRow(t, x, y, i)
	}
	return t
}

func fillRow(t [][]int, x, y []string, i int) {
	for j := len(y) - 1; j >= 0; j-- {
		t[i][j] = max(t[i+1][j], t[i][j+1])
		if x[i] == y[j] {
			t[i][j] = t[i+1][j+1] + 1
		}
	}
}

// elide keeps changed lines and diffContext lines around them.
func elide(ops []string) []string {
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if strings.HasPrefix(op, "  ") {
			continue
		}
		for k := max(0, i-diffContext); k <= min(len(ops)-1, i+diffContext); k++ {
			keep[k] = true
		}
	}
	var out []string
	for i, op := range ops {
		if keep[i] {
			out = append(out, op)
			continue
		}
		if len(out) == 0 || out[len(out)-1] != "  ..." {
			out = append(out, "  ...")
		}
	}
	return out
}
