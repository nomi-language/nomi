package vmhost_test

import (
	"sync"
	"testing"

	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// TestStdlibCheck_RunsBesideALowering checks a stdlib module's source
// (LoadStdlibSource) while other goroutines load programs whose lowering
// checks std modules' bodies (std.StdLib.Bodies), which writes the shared
// analyses the std check reads. The check takes vmhost's lowering lock, so
// under -race the two never touch those analyses at once.
func TestStdlibCheck_RunsBesideALowering(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; checks std modules and lowers programs; -short")
	}
	pinStdlibEnv(t)
	programs := []string{
		"import\n    std::csv\n    std::io\nend\n\nfn main()\n    io.print(csv.decode(\"a,b\\n1,2\\n\"))\nend\n",
		"import\n    std::io\n    std::xml\nend\n\nfn main()\n    io.print(xml.xml_namespace())\nend\n",
		"import\n    std::io\n    std::url\nend\n\nfn main()\n    io.print(url.escape_query(\"a b\"))\nend\n",
	}
	src, ok := std.ReadFile("regex")
	if !ok {
		t.Fatal("std/regex is not embedded")
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(programs)+2)
	for _, p := range programs {
		wg.Go(func() {
			if _, err := vmhost.LoadSource("main", p); err != nil {
				errs <- err
			}
		})
	}
	for range 2 {
		wg.Go(func() {
			if _, err := vmhost.LoadStdlibSource("regex", string(src)); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
