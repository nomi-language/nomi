package lsp

import (
	"sync"

	"github.com/nomi-language/nomi/std"
)

// loadStd is this test binary's one std.Load: the stdlib analyzed from
// source, which the tests only read. Every std.Load re-parses and
// re-analyzes every stdlib module, so a fixture helper that loaded per case
// paid that for a check of a few lines.
var loadStd = sync.OnceValue(std.Load)
