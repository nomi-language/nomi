package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/mcp"

	"github.com/tliron/commonlog"
)

// runMCP serves `nomi mcp`: the Model Context Protocol on stdin and stdout,
// for a coding agent (internal/mcp). Stdout carries only the protocol, so
// the language server inside logs to stderr.
func runMCP(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: nomi mcp")
		return 1
	}
	commonlog.Configure(0, nil)
	s := mcp.New(mcp.Options{Version: imageWriterVersion()})
	if err := s.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
