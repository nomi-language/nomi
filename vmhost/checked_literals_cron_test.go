package vmhost

import "testing"

// A Cron literal is checked when the program is checked: an expression
// Cron.parse refuses is a compile error at the character it names, or at
// the literal when it names the whole expression, and a slot is one at its
// `${`.
func TestCheckedLiteral_CronRefusesBadExpressionsAndSlots(t *testing.T) {
	for _, c := range []struct {
		line, message string
		col           int
	}{
		{"    _ = Cron`0 25 * * *`", "typed literal Cron`0 25 * * *` is invalid: hour 25 is out of range 0-23", 16},
		{"    _ = Cron`0 9 * * MONDAY`", "typed literal Cron`0 9 * * MONDAY` is invalid: MONDAY is not a day of week", 22},
		{"    _ = Cron`*/0 * * * *`", "typed literal Cron`*/0 * * * *` is invalid: a step is a whole number from 1, not 0", 16},
		{"    _ = Cron`0 0 31 4,6 *`", "typed literal Cron`0 0 31 4,6 *` is invalid: no month chosen has the days of the month chosen", 18},
		{"    _ = Cron`0 3 * *`", "typed literal Cron`0 3 * *` is invalid: a cron expression has five fields (minute, hour, day of month, month, day of week), not 4", 9},
		{"    _ = Cron\"0 ${v} * * *\"", "typed literal Cron\"0 ${v} * * *\" is invalid: a cron expression takes no slots", 16},
	} {
		src := "import\n    std::io\n    std::jobs.Cron\nend\n\nfn main()\n    v = \"3\"\n    io.print(v)\n" + c.line + "\nend\n"
		ds := literalDiagnostics(t, src)
		if len(ds) != 1 {
			t.Fatalf("%s: %d diagnostics, want 1: %v", c.line, len(ds), ds)
		}
		if ds[0].Message != c.message {
			t.Errorf("%s: message = %q, want %q", c.line, ds[0].Message, c.message)
		}
		if ds[0].Line != 9 || ds[0].Col != c.col {
			t.Errorf("%s: at %d:%d, want 9:%d", c.line, ds[0].Line, ds[0].Col, c.col)
		}
	}
}
