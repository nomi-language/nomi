package main

import (
	"github.com/nomi-language/nomi/internal/lsp"

	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
)

func main() {
	commonlog.Configure(1, nil)
	server := lsp.NewServer()
	server.RunStdio()
}
