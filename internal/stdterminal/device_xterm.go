//go:build !windows

package stdterminal

import "golang.org/x/term"

// isTerminal is whether fd is a terminal, as golang.org/x/term finds it:
// whether it has termios on a Unix system, and no on js/wasm and wasip1.
func isTerminal(fd uintptr) bool { return term.IsTerminal(int(fd)) }

// size is the columns and rows of the terminal fd is.
func size(fd uintptr) (columns, rows int, err error) { return term.GetSize(int(fd)) }
