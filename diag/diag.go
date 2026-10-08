// Package diag formats error messages with the offending source line.
package diag

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Username59138/sepl/token"
)

// Format renders an error like:
//
//	main.sepl:3:5: error: unexpected ')': nothing to close
//	 3 | print(x))
//	   |         ^
func Format(filename, src string, pos token.Pos, msg string) string {
	return FormatLabel(filename, src, pos, "error", msg)
}

// FormatLabel is Format with another label than "error" (e.g. "runtime error").
func FormatLabel(filename, src string, pos token.Pos, label, msg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:%d: %s: %s\n", filename, pos.Line, pos.Col, label, msg)

	lines := strings.Split(src, "\n")
	if pos.Line < 1 || pos.Line > len(lines) {
		return b.String()
	}
	line := strings.TrimRight(lines[pos.Line-1], "\r")
	num := strconv.Itoa(pos.Line)
	fmt.Fprintf(&b, " %s | %s\n", num, line)

	// Keep tabs in the padding so the caret lines up with the source.
	var pad strings.Builder
	col := 1
	for _, r := range line {
		if col >= pos.Col {
			break
		}
		if r == '\t' {
			pad.WriteByte('\t')
		} else {
			pad.WriteByte(' ')
		}
		col++
	}
	for ; col < pos.Col; col++ {
		pad.WriteByte(' ')
	}
	fmt.Fprintf(&b, " %s | %s^\n", strings.Repeat(" ", len(num)), pad.String())
	return b.String()
}
