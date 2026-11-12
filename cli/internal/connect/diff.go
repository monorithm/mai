package connect

import (
	"fmt"
	"strings"
)

const diffContext = 3

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type diffOp struct {
	kind byte // ' ', '-', or '+'
	text string
}

// lineOps compares a and b line by line. It removes the common start and end first, then
// finds the longest common subsequence of the middle part.
func lineOps(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	lcs := make([][]int32, len(ma)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(mb)+1)
	}
	for i := len(ma) - 1; i >= 0; i-- {
		for j := len(mb) - 1; j >= 0; j-- {
			if ma[i] == mb[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []diffOp
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	i, j := 0, 0
	for i < len(ma) || j < len(mb) {
		switch {
		case i < len(ma) && j < len(mb) && ma[i] == mb[j]:
			ops = append(ops, diffOp{' ', ma[i]})
			i++
			j++
		case j < len(mb) && (i == len(ma) || lcs[i][j+1] >= lcs[i+1][j]):
			ops = append(ops, diffOp{'+', mb[j]})
			j++
		default:
			ops = append(ops, diffOp{'-', ma[i]})
			i++
		}
	}
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

// unifiedDiff returns the changes from a to b in the unified format, or "" when a equals b.
func unifiedDiff(fromName, toName, a, b string) string {
	if a == b {
		return ""
	}
	ops := lineOps(splitLines(a), splitLines(b))
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", fromName, toName)
	for k := 0; k < len(ops); {
		if ops[k].kind == ' ' {
			k++
			continue
		}
		start := max(k-diffContext, 0)
		end := k
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*diffContext || run == len(ops) {
				end = min(end+diffContext, len(ops))
				break
			}
			end = run
		}
		aLine, bLine := 1, 1
		for _, op := range ops[:start] {
			if op.kind != '+' {
				aLine++
			}
			if op.kind != '-' {
				bLine++
			}
		}
		aN, bN := 0, 0
		for _, op := range ops[start:end] {
			if op.kind != '+' {
				aN++
			}
			if op.kind != '-' {
				bN++
			}
		}
		if aN == 0 {
			aLine--
		}
		if bN == 0 {
			bLine--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aLine, aN, bLine, bN)
		for _, op := range ops[start:end] {
			out.WriteByte(op.kind)
			out.WriteString(op.text)
			if !strings.HasSuffix(op.text, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		k = end
	}
	return out.String()
}
