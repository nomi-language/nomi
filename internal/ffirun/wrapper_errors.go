package ffirun

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

const wrapperBinaryBaseName = "nomi-ffi-wrapper"

func buildWrapperBinary(r *Result, outputPath string) error {
	// The same account of a missing toolchain `nomi build` gives, from the same
	// place. This site's own message was `ffirun: building wrapper: exec: "go":
	// executable file not found in $PATH` — true, and it named neither the PATH
	// it searched nor the fact that a toolchain-free `nomi run` exists for
	// projects without Go FFI.
	//
	// The FFI-wrapper spelling, not `nomi build`'s: naming `nomi build` inside
	// a failing `nomi run` reads as an unrelated suggestion, and this path
	// carries no generated module whose `go` directive could be quoted.
	goBin, err := gotoolchain.FindForFFIWrapper()
	if err != nil {
		return fmt.Errorf("ffirun: %w", err)
	}
	cmd := exec.Command(goBin, "build", "-mod=mod", "-o", outputPath, r.WrapperPath)
	cmd.Dir = r.WrapperDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return wrapperCommandError(r, "building wrapper", err, out)
	}
	return nil
}

func ensureCachedWrapperBinary(r *Result) (string, error) {
	path := cachedWrapperBinaryPath(r)
	binaryInfo, binaryErr := os.Stat(path)
	wrapperInfo, wrapperErr := os.Stat(r.WrapperPath)
	if wrapperErr != nil {
		return "", fmt.Errorf("ffirun: stat wrapper source: %w", wrapperErr)
	}
	if binaryErr == nil && !binaryInfo.ModTime().Before(wrapperInfo.ModTime()) {
		return path, nil
	}
	if binaryErr != nil && !os.IsNotExist(binaryErr) {
		return "", fmt.Errorf("ffirun: stat wrapper binary: %w", binaryErr)
	}
	tmp, err := os.CreateTemp(r.WrapperDir, wrapperBinaryBaseName+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("ffirun: creating wrapper binary temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("ffirun: closing wrapper binary temp file: %w", err)
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", fmt.Errorf("ffirun: preparing wrapper binary temp file: %w", err)
	}
	if err := buildWrapperBinary(r, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("ffirun: caching wrapper binary: %w", err)
	}
	return path, nil
}

func cachedWrapperBinaryPath(r *Result) string {
	return cachedWrapperBinaryPathForDir(r.WrapperDir)
}

func cachedWrapperBinaryPathForDir(dir string) string {
	name := wrapperBinaryBaseName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// wrapperReport is what a failed wrapper command printed, cleaned: its
// diagnostics or the Go toolchain's, each line standing on its own.
type wrapperReport string

func (e wrapperReport) Error() string { return string(e) }

// OwnLines marks the report as lines that each stand on their own
// (rt.WriteFailLine).
func (e wrapperReport) OwnLines() bool { return true }

func wrapperCommandError(r *Result, action string, err error, out []byte) error {
	cleaned := cleanWrapperCommandOutput(r, string(out))
	if cleaned == "" {
		return fmt.Errorf("ffirun: %s: %w", action, err)
	}
	return wrapperReport(cleaned)
}

func cleanWrapperCommandOutput(r *Result, out string) string {
	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "# command-line-arguments", "# nomi-ffi-wrapper", "# nomi-build-wrapper":
			continue
		}
		kept = append(kept, cleanWrapperCommandLine(r, line))
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\r\n")
}

func cleanWrapperCommandLine(r *Result, line string) string {
	if r == nil {
		return line
	}
	replacements := []struct {
		old string
		new string
	}{
		{r.WrapperPath, "<generated-ffi-wrapper>/main.go"},
		{filepath.ToSlash(r.WrapperPath), "<generated-ffi-wrapper>/main.go"},
		{r.WrapperDir, "<generated-ffi-wrapper>"},
		{filepath.ToSlash(r.WrapperDir), "<generated-ffi-wrapper>"},
	}
	for _, repl := range replacements {
		if repl.old != "" {
			line = strings.ReplaceAll(line, repl.old, repl.new)
		}
	}
	return line
}
