package rt

import (
	"strconv"
	"strings"
)

// UnifiedDiff is the unified diff that turns before into after, or "" when
// the two are equal. `nomi fmt -d` prints it for each file the formatter
// would change. It is the line diff a failed String assertion shows
// (lineDiff), written in the form `diff -u` and `patch` read: a `--- name`
// and a `+++ name` line, then one hunk per group of changes, each with
// diffContext unchanged lines around it and a `@@ -start,count +start,count
// @@` header. A count of one is left out, and a side with no lines in the
// hunk counts from the line before it. A line with no final newline is
// followed by `\ No newline at end of file`. A line's text is written as it
// is, so a line that ends in a carriage return keeps it.
//
// When the changed lines are too many to compare (diffMaxCells), the diff
// removes every line between the common prefix and suffix and adds every
// new one: a hunk patch applies the same way, only longer.
func UnifiedDiff(name, before, after string) string {
	if before == after {
		return ""
	}
	ops, _ := lineDiff(splitDiffLines(before), splitDiffLines(after))
	shown := changeContext(ops)
	var b strings.Builder
	b.WriteString("--- " + name + "\n+++ " + name + "\n")
	// oldAt and newAt count the lines of each side before ops[i].
	oldAt, newAt := 0, 0
	for i := 0; i < len(ops); {
		if !shown[i] {
			oldAt++
			newAt++
			i++
			continue
		}
		end := i
		oldCount, newCount := 0, 0
		for ; end < len(ops) && shown[end]; end++ {
			if ops[end].kind != '+' {
				oldCount++
			}
			if ops[end].kind != '-' {
				newCount++
			}
		}
		b.WriteString("@@ -" + hunkRange(oldAt, oldCount) + " +" + hunkRange(newAt, newCount) + " @@\n")
		for _, op := range ops[i:end] {
			b.WriteByte(op.kind)
			b.WriteString(op.line.text)
			b.WriteByte('\n')
			if !op.line.nl {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
		oldAt += oldCount
		newAt += newCount
		i = end
	}
	return b.String()
}

// hunkRange is one side's `start,count` in a hunk header, given how many of
// the side's lines come before the hunk.
func hunkRange(before, count int) string {
	switch count {
	case 0:
		return strconv.Itoa(before) + ",0"
	case 1:
		return strconv.Itoa(before + 1)
	}
	return strconv.Itoa(before+1) + "," + strconv.Itoa(count)
}
