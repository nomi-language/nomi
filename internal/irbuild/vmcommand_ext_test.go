package irbuild_test

import (
	"bytes"

	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vmcmd"
	"github.com/nomi-language/nomi/vmhost"
)

// init installs irbuild.VMCommand: internal/vmcmd, which `nomi run` and
// `nomi test` call, over buffers.
func init() {
	irbuild.VMCommand = func(path string) (string, string, int) {
		var out, errOut bytes.Buffer
		if hasTests, err := vmhost.FileDeclaresTests(path); err == nil && hasTests {
			restore, err := vmhost.DefaultTestEnv()
			if err != nil {
				return "", err.Error() + "\n", 1
			}
			defer restore()
			rep := vmhost.NewTestReport(&out)
			tester := &vmcmd.Tester{Out: &out, Stderr: &errOut, Stdin: bytes.NewReader(nil), Rep: rep}
			tester.File(path, vmhost.TestOptions{})
			exit := 0
			if rep.Summary() {
				exit = 1
			}
			return out.String(), errOut.String(), exit
		}
		exit := vmcmd.Run(path, &out, &errOut, bytes.NewReader(nil), nil, false)
		return out.String(), errOut.String(), exit
	}
}
