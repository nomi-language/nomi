//go:build !(darwin || linux)

package main

import "testing"

// TestTerminal_OnAPseudoTerminal is terminal_pty_test.go's, which opens a
// pseudo-terminal through /dev/ptmx: Windows has no such device (its
// pseudo-console is a different interface), so std::terminal's modes are
// not exercised here.
func TestTerminal_OnAPseudoTerminal(t *testing.T) {
	t.Skip("no /dev/ptmx pseudo-terminal on this system")
}
