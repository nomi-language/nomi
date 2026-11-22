package vmhost

import (
	"strings"
	"testing"
)

// handleFiles is an app that makes a value and hands it to a dependency,
// keep, whose entry under [effects.deps.modules] allows nothing: main is
// the app's code, and keep.nomi the dependency's. The app's own list
// allows std.
func handleFiles(std, main, keep string) map[string]string {
	return map[string]string{
		"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\nkeep = { dir = \"../keep\" }\n\n" +
			"[effects.self]\nstd = " + std + "\n\n[effects.deps.modules]\n\"keep\" = []\n",
		"app/main.nomi":  main,
		"keep/nomi.toml": "[module]\nname = \"keep\"\n",
		"keep/keep.nomi": keep,
	}
}

// keepSaves is a dependency that puts an object in the Store it is
// handed, through the interface.
const keepSaves = `import std::storage.Store

pub fn save(store: Store) -> String
    case Store.put(store, "a.txt", String.to_bytes("hi"), "text/plain")
        Ok(_) -> "saved"
        Err(e) -> "${e}"
    end
end
`

// A call through an interface acts only on the value it is given, as a
// held crossing does: what a std implementation does is named where the
// value is made, at the call into std that makes it. A dependency handed
// a store the app made with storage.local puts through it with an empty
// entry, though std::storage's Local and std::storage::testing's Memory
// both implement Store; the app's own list must name std::storage, at its
// call of storage.local, and nothing names std::storage::testing, whose
// stores the program never makes.
func TestEffectHandles_ACallThroughAnInterfaceIsNamedWhereTheValueIsMade(t *testing.T) {
	main := `import
    std::io
    std::storage
    keep
end

fn main()
    io.print(keep.save(storage.local("files")))
end
`
	checkedRootsPass(t, "app/main.nomi", handleFiles(`["std::io", "std::storage"]`, main, keepSaves))
	got, _ := effectDiagnostics(t, "app/main.nomi", handleFiles(`["std::io"]`, main, keepSaves))
	want := "app/main.nomi:8:24: this module's own code has the effect std::storage, and [effects.self] allows it only std::io\n" +
		"app/main.nomi:8:24: help: reached from `main`\n" +
		"app/main.nomi:8:24: help: add \"std::storage\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// A body outside std that a dispatch reaches is named in its own file:
// the dependency's own Disk, which writes a file, makes its call through
// its own interface reach std::fs in its file.
func TestEffectHandles_AnImplOutsideStdIsNamedInItsFile(t *testing.T) {
	keep := `import std::fs

pub interface Sink
    fn put(sink: self, key: String) -> String
end

pub fn save(sink: Sink) -> String
    Sink.put(sink, "a.txt")
end

pub fn disk() -> Sink
    Disk{}
end

struct Disk
end

impl Sink for Disk
    fn put(_sink: Disk, key: String) -> String
        case fs.write_text(key, "hi")
            Ok(_) -> "saved"
            Err(_) -> "failed"
        end
    end
end
`
	main := `import
    std::io
    keep
end

fn main()
    io.print(keep.save(keep.disk()))
end
`
	got, _ := effectDiagnostics(t, "app/main.nomi", handleFiles(`["std::io"]`, main, keep))
	want := "keep/keep.nomi:20:14: keep has the effect std::fs, and its entry under [effects.deps.modules] allows it no effects\n" +
		"keep/keep.nomi:20:14: help: reached from `main`; `main` calls `save`; `save` calls `put` through an interface, and any implementation of it counts, including `Sink.put for Disk`\n" +
		"keep/keep.nomi:20:14: help: add \"std::fs\" to \"keep\" under [effects.deps.modules] in nomi.toml, or remove the call\n" +
		"app/main.nomi:7:14: note: your code has the effect std::fs in keep here: `main` calls `save`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// Making a client is the network effect, named where the client is made;
// sending through one already made adds no name. A dependency handed the
// app's client fetches with an empty entry, and one that makes its own
// client is refused at the making.
func TestEffectHandles_AClientIsAHandle(t *testing.T) {
	main := `import
    std::http.Client
    std::io
    std::url.Url
    keep
end

fn main()
    io.print(keep.status(Client.new(), Url"https://example.com/"))
end
`
	status := func(client string) string {
		return `import
    std::http.Client
    std::url.Url
end

pub fn status(client: Client, url: Url) -> String
    case Client.get(` + client + `, url)
        Ok(r) -> "${r.status}"
        Err(_) -> "offline"
    end
end
`
	}
	std := `["std::io", "std::http"]`
	checkedRootsPass(t, "app/main.nomi", handleFiles(std, main, status("client")))
	got, _ := effectDiagnostics(t, "app/main.nomi", handleFiles(std, main, strings.Replace(status("Client.new()"), "(client: Client,", "(_client: Client,", 1)))
	want := "keep/keep.nomi:7:21: keep has the effect std::http, and its entry under [effects.deps.modules] allows it no effects\n" +
		"keep/keep.nomi:7:21: help: reached from `main`; `main` calls `status`\n" +
		"keep/keep.nomi:7:21: help: add \"std::http\" to \"keep\" under [effects.deps.modules] in nomi.toml, or remove the call\n" +
		"app/main.nomi:9:14: note: your code has the effect std::http in keep here: `main` calls `status`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}
