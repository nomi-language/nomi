package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A quick fix a macro offers may run one of its module's tools
// (std/macro's Action.tool): `nomi tool <module> <name> <args>` in the
// declaring module's root, such as sqlkit's `prepare`, which describes the
// queries against a database and writes the snapshot the macro reads.
// A tool has every effect, so the server runs one only through the code
// action's command, when the person chooses the fix, and only a run it
// offered. Once the tool has run, the open documents are analyzed again
// with their inputs read from disk, and the module's closed files are
// checked again, as when an input changes.

// runToolCommand is the command a tool-running code action carries. Its
// arguments are the module root, the macro's module, the tool's name
// and the tool's arguments.
const runToolCommand = "nomi.runMacroTool"

// toolTimeout bounds one tool run.
const toolTimeout = 5 * time.Minute

// macroTools are the tool runs the server has offered, by their
// command arguments, and how a run is made.
type macroTools struct {
	mu      sync.Mutex
	offered map[string]bool
	// run runs `nomi tool args...` in dir and answers its output; tests
	// replace it.
	run func(ctx context.Context, dir string, args []string) (string, error)
}

func toolKey(args []string) string {
	return strings.Join(args, "\x00")
}

// offer records a run the server hands out and answers the command that
// makes it.
func (t *macroTools) offer(title, root, module, tool string, args []string) *protocol.Command {
	command := append([]string{root, module, tool}, args...)
	t.mu.Lock()
	if t.offered == nil {
		t.offered = map[string]bool{}
	}
	t.offered[toolKey(command)] = true
	t.mu.Unlock()
	arguments := make([]any, len(command))
	for i, a := range command {
		arguments[i] = a
	}
	return &protocol.Command{Title: title, Command: runToolCommand, Arguments: arguments}
}

// runNomiTool is the run a server makes: the `nomi` beside the running
// binary (nomi-lsp is installed beside it), else the one on PATH.
func runNomiTool(ctx context.Context, dir string, args []string) (string, error) {
	bin := ""
	if self, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(self), "nomi")
		if info, err := os.Stat(sibling); err == nil && !info.IsDir() {
			bin = sibling
		}
	}
	if bin == "" {
		path, err := exec.LookPath("nomi")
		if err != nil {
			return "", fmt.Errorf("no `nomi` beside nomi-lsp or on PATH to run `nomi tool %s`", strings.Join(args, " "))
		}
		bin = path
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"tool"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// workspaceExecuteCommand runs a tool-running code action's command, says
// what it printed, and checks again what may read what it wrote.
func (s *Server) workspaceExecuteCommand(_ *glsp.Context, params *protocol.ExecuteCommandParams) (any, error) {
	if params.Command != runToolCommand {
		return nil, fmt.Errorf("unknown command %q", params.Command)
	}
	args := make([]string, len(params.Arguments))
	for i, a := range params.Arguments {
		text, ok := a.(string)
		if !ok {
			return nil, fmt.Errorf("%s: argument %d is not a string", runToolCommand, i+1)
		}
		args[i] = text
	}
	s.tools.mu.Lock()
	offered := s.tools.offered[toolKey(args)]
	run := s.tools.run
	s.tools.mu.Unlock()
	if !offered || len(args) < 3 {
		return nil, fmt.Errorf("%s: not a tool run this server offered", runToolCommand)
	}
	if run == nil {
		run = runNomiTool
	}
	root, toolArgs := args[0], args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	out, err := run(ctx, root, toolArgs)
	message := strings.TrimSpace(out)
	kind := protocol.MessageTypeInfo
	if err != nil {
		kind = protocol.MessageTypeError
		message = strings.TrimSpace(fmt.Sprintf("`nomi tool %s` failed: %v\n%s", strings.Join(toolArgs, " "), err, message))
	} else if message == "" {
		message = "`nomi tool " + strings.Join(toolArgs, " ") + "` ran"
	}
	if s.notify != nil {
		s.notify(protocol.ServerWindowShowMessage, &protocol.ShowMessageParams{Type: kind, Message: message})
	}
	// Every open document is analyzed again with its programs' inputs read
	// from disk, a file an expansion asked for that did not exist until the
	// tool wrote it among them, and the module's closed files are queued.
	open, closed := s.docs.InputsChanged(filepath.Join(root, "nomi.toml"))
	s.queueClosedDiagnostics(closed...)
	if s.notify != nil {
		for _, doc := range open {
			s.publish(s.notify, doc.URI)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s", message)
	}
	return nil, nil
}
