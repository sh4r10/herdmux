package install

import (
	"fmt"
	"strings"
)

// unifiedDiff returns a unified diff (3 lines of context) from a to b, or ""
// when they are equal. A missing side is named /dev/null.
func unifiedDiff(aName, bName string, a, b []byte) string {
	if string(a) == string(b) {
		return ""
	}
	al, bl := splitLines(string(a)), splitLines(string(b))
	ops := diffLines(al, bl)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", aName, bName)
	const ctx = 3
	// Group ops into hunks around the changes.
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(i-ctx, 0)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			// A run of equal lines: end the hunk if it is longer than 2*ctx
			// or reaches the end.
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run == len(ops) || run-end > 2*ctx {
				end = min(end+ctx, len(ops))
				break
			}
			end = run
		}
		writeHunk(&out, ops[start:end])
		i = end
	}
	return out.String()
}

type diffOp struct {
	kind       byte // ' ', '-', '+'
	line       string
	aIdx, bIdx int // 1-based line numbers in a and b at this op
}

func writeHunk(out *strings.Builder, ops []diffOp) {
	var aStart, bStart, aN, bN int
	aStart, bStart = ops[0].aIdx, ops[0].bIdx
	for _, op := range ops {
		switch op.kind {
		case ' ':
			aN++
			bN++
		case '-':
			aN++
		case '+':
			bN++
		}
	}
	if aN == 0 {
		aStart--
	}
	if bN == 0 {
		bStart--
	}
	fmt.Fprintf(out, "@@ -%d,%d +%d,%d @@\n", aStart, aN, bStart, bN)
	for _, op := range ops {
		out.WriteByte(op.kind)
		out.WriteString(op.line)
		if !strings.HasSuffix(op.line, "\n") {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
}

// splitLines keeps each line's "\n".
func splitLines(s string) []string {
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// diffLines is an LCS line diff. The common prefix and suffix are trimmed
// first, so the quadratic part only covers the changed middle.
func diffLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]
	// lcs[i][j] is the LCS length of am[i:] and bm[j:].
	lcs := make([][]int, len(am)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(bm)+1)
	}
	for i := len(am) - 1; i >= 0; i-- {
		for j := len(bm) - 1; j >= 0; j-- {
			if am[i] == bm[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	ai, bi := 1, 1
	add := func(kind byte, line string) {
		ops = append(ops, diffOp{kind, line, ai, bi})
		if kind != '+' {
			ai++
		}
		if kind != '-' {
			bi++
		}
	}
	for _, l := range a[:pre] {
		add(' ', l)
	}
	i, j := 0, 0
	for i < len(am) || j < len(bm) {
		switch {
		case i < len(am) && j < len(bm) && am[i] == bm[j]:
			add(' ', am[i])
			i++
			j++
		case i < len(am) && (j == len(bm) || lcs[i+1][j] >= lcs[i][j+1]):
			add('-', am[i])
			i++
		default:
			add('+', bm[j])
			j++
		}
	}
	for _, l := range a[len(a)-suf:] {
		add(' ', l)
	}
	return ops
}
