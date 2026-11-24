package vmhost_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A program that reads keys and a password builds for every target:
// std::terminal's crossings carry no effect class, and where there is no
// terminal they answer NotATerminal as the program runs, as `io.read_line`
// answers the end of the input.
func TestCheckTarget_TerminalProgramsBuildForEveryTarget(t *testing.T) {
	src := `import
    std::io
    std::terminal.{self, RawMode}
end

fn main() -> Result<Unit, terminal.Error>
    if io.is_terminal(.Stdout) then io.print(Debug.inspect(terminal.size()))
    _ = terminal.read_password(prompt: "password: ")?
    raw = terminal.raw_mode()?

    defer RawMode.restore(raw)

    _ = terminal.read_key()?
    Ok(Unit)
end
`
	path := filepath.Join(t.TempDir(), "keys.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range [][2]string{{"linux", "amd64"}, {"windows", "amd64"}, {"js", "wasm"}, {"wasip1", "wasm"}} {
		command := "nomi build --target " + target[0] + "/" + target[1]
		if refused := p.CheckTarget(command, target[0], target[1], false, nil); refused != nil {
			t.Errorf("%s refuses a program that uses the terminal:\n%s", command, refused.Error())
		}
	}
}
