package vm

import "testing"

// A crossing deep in std is reported as the std function the program
// called, `fs.read_text`, whichever system's path the stdlib was loaded
// from.
func TestStdModuleOf_NamesTheModuleOnEverySystem(t *testing.T) {
	for file, want := range map[string]string{
		"std/fs.nomi":                     "fs",
		"/home/me/nomi/std/fs.nomi":       "fs",
		`D:\a\nomi\nomi\std\fs.nomi`:      "fs",
		`C:\Users\me\std/fs_testing.nomi`: "fs_testing",
		"fs.nomi":                         "fs",
	} {
		if got := stdModuleOf(file); got != want {
			t.Errorf("stdModuleOf(%q) = %q, want %q", file, got, want)
		}
	}
}

// The scans of a program's currency, skeleton and message literals find
// std's modules by name, whichever system's path the stdlib was loaded
// from: on Windows a built image carried no currency symbols and no
// resolved date skeletons when they did not.
func TestStdModule_FindsAStdModuleByAnySystemsPath(t *testing.T) {
	for _, c := range []struct {
		name, module string
		want         bool
	}{
		{"std/money.nomi", MoneyModule, true},
		{"/home/me/nomi/std/money.nomi", MoneyModule, true},
		{`D:\a\nomi\nomi\std\money.nomi`, MoneyModule, true},
		{`D:\a\nomi\nomi\std\calendar.nomi`, CalendarModule, true},
		{`C:\work\std\phrase.nomi`, PhraseModule, true},
		{`D:\a\std\money_format.nomi`, MoneyModule, false},
		{"/home/me/mystd/money.nomi", MoneyModule, false},
		{"money.nomi", MoneyModule, false},
	} {
		if got := stdModule(c.name, c.module); got != c.want {
			t.Errorf("stdModule(%q, %q) = %v, want %v", c.name, c.module, got, c.want)
		}
	}
}
