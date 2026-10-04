package irbuild

import (
	"github.com/nomi-language/nomi/internal/compilerhosts"
	"github.com/nomi-language/nomi/internal/vm"
)

// testHosts is std/compiler's hosts, as vmhost binds them, over no project
// root. A machine answers every other stdlib host from its own generated
// adapters.
var testHosts vm.HostTable = compilerhosts.Table("", nil)
