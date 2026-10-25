package vmhost

import (
	"context"
	"strings"
	"testing"
)

// Debug renders a struct whose field is an interface value when the file
// that declares the struct and the interface implements it for nothing and
// another file implements and boxes it: the renderer names the implementers
// of every file of the program, not only the declaring one's.
func TestCrossFileExistentialDebug_RendersAnotherFilesImplementer(t *testing.T) {
	entry, name, files, manifest, err := SplitMultiFile(`// FILE: named.nomi
pub interface Named {
    fn name(n: self) -> String
}

pub struct Holder {
    held: Named
}

// FILE: main.nomi
import {
    std/io
    named.{Holder, Named}
}

struct Mark {
    k: Int
}

impl Named for Mark {
    fn name(m: Mark) -> String {
        "m${m.k}"
    }
}

fn main() {
    h = #Holder{held: #Mark{k: 1}}
    io.print(Named.name(h.held))
    io.print(Debug.inspect(h))
}
`)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	p, err := LoadSource(name, entry, WithVirtualFiles(files), WithVirtualManifest(manifest))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	if err := p.Unsupported(); err != nil {
		t.Fatalf("the program does not lower: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if want := "m1\n#Holder{held: #Mark{k: 1}}\n"; out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}
