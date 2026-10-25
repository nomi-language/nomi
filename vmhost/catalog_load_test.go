package vmhost

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/mf2"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmrunner"
)

// catalogProject is a project whose main loads a catalog at run time: one
// accepted entry, one stale, one for a message main never reaches, and one
// with an enum arm missing.
var catalogProject = map[string]string{
	"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[i18n]\nlocales = [\"en\", \"pl\"]\n",
	"text.nomi": "pub enum Status {\n    Open\n    Closed\n}\n\n" +
		"pub message items(count: Int) `\n    .input {$count :integer}\n    .match $count\n    one {{{$count} item}}\n    * {{{$count} items}}\n    `\n\n" +
		"pub message status(status: Status) `\n    .input {$status :string}\n    .match $status\n    Open {{open}}\n    Closed {{closed}}\n    * {{open}}\n    `\n\n" +
		"pub message unused(name: String) `{$name} is never shown`\n",
	"locales/pl/text.toml": "[items]\nsource = '''\n.input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}\n'''\n" +
		"text = '''\n.input {$count :integer}\n.match $count\none {{{$count} rzecz}}\nfew {{{$count} rzeczy}}\nmany {{{$count} rzeczy}}\n* {{{$count} rzeczy}}\n'''\n\n" +
		"[status]\nsource = '''\n.input {$status :string}\n.match $status\nOpen {{open}}\nClosed {{closed}}\n* {{open}}\n'''\n" +
		"text = '''\n.input {$status :string}\n.match $status\nOpen {{otwarte}}\nClosed {{zamknięte}}\n* {{otwarte}}\n'''\n\n" +
		"[unused]\nsource = '''{$name} is never shown'''\ntext = '''{$name} nie jest pokazywane'''\n",
	"main.nomi": "import {\n    std/io\n    std/locale.Locale\n    std/message.{Catalog, Message}\n    text\n}\n\n" +
		"fn main() {\n    loaded = \"\"\"\n" +
		"    [text.items]\n    source = '''\n    .input {$count :integer}\n    .match $count\n    one {{{$count} item}}\n    * {{{$count} items}}\n    '''\n" +
		"    text = '''\n    .input {$count :integer}\n    .match $count\n    one {{{$count} sztuka}}\n    few {{{$count} sztuki}}\n    many {{{$count} sztuk}}\n    * {{{$count} sztuki}}\n    '''\n\n" +
		"    [text.status]\n    source = '''\n    .input {$status :string}\n    .match $status\n    Open {{open}}\n    Closed {{closed}}\n    * {{open}}\n    '''\n" +
		"    text = '''\n    .input {$status :string}\n    .match $status\n    Open {{czynne}}\n    * {{czynne}}\n    '''\n\n" +
		"    [text.unused]\n    source = '''{$name} is never shown'''\n    text = '''{$name} nigdy'''\n" +
		"    \"\"\"\n" +
		"    #(catalog, rejected) = Catalog.load(loaded, Locale`pl`)\n" +
		"    Iter.each(rejected, fn r -> io.print(\"${r.key}: ${r.reason}\"))\n" +
		"    io.print(text.items(3) |> Message.format(Locale`pl`, catalogs: [catalog]))\n" +
		"    io.print(text.status(text.Status.Closed) |> Message.format(Locale`pl`, catalogs: [catalog]))\n" +
		"}\n",
}

// A catalog loaded at run time is checked against the signatures the
// program was compiled with, the enum's variants included, and a built
// program carries only the tables of the messages it reaches, so it
// accepts and rejects the entries `nomi run` does.
func TestCatalogLoad_ABuiltProgramChecksAsTheProgramDoes(t *testing.T) {
	dir := t.TempDir()
	for name, text := range catalogProject {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(filepath.Join(dir, "main.nomi"))
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	want := "text.status: `status` has no `Closed` arm; `.match $status` needs an arm for every variant of `Status`\n" +
		"text.unused: `text.unused` is not a message or noun the program uses\n" +
		"3 sztuki\n" +
		"zamknięte\n"
	var inProcess bytes.Buffer
	if err := p.Run(context.Background(), &inProcess, nil, false); err != nil {
		t.Fatal(err)
	}
	if inProcess.String() != want {
		t.Errorf("in process:\n%s\nwant:\n%s", inProcess.String(), want)
	}
	image, _, err := p.BuildImage()
	if err != nil {
		t.Fatal(err)
	}
	var built, stderr bytes.Buffer
	if code := vmrunner.Run(image, &built, &stderr, nil, nil, false); code != 0 || built.String() != want {
		t.Errorf("the built image exits %d and prints:\n%s%s\nwant:\n%s", code, built.String(), stderr.String(), want)
	}

	// The image's tables are the reached messages', with their parameters'
	// resolved types.
	im, err := ir.DecodeImage(image)
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := vm.MessageTables(im.Modules, vm.ModuleFuncs(im.Modules), func(name string) bool { return name == vm.MessageModule })
	var names []string
	for _, text := range messages {
		table, err := mf2.DecodeTable(text)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, table.Name)
		if table.Name == "text.status" {
			got := table.Params[0]
			if got.Kind != mf2.KindEnum || strings.Join(got.Variants, ",") != "Open,Closed" || got.Type != "Status" {
				t.Errorf("status's parameter is carried as %+v", got)
			}
		}
	}
	if strings.Join(names, ",") != "text.items,text.status" {
		t.Errorf("the image carries the tables of %v, want items and status", names)
	}
}
