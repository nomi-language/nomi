package vmhost_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// timedFiles is a FileSystem whose every Stat answers the modification
// time `at`, as a file system that records times past 2262, or none, does.
type timedFiles struct {
	vmhost.FileSystem
	at time.Time
}

func (f timedFiles) Stat(p string) (vmhost.FileInfo, error) {
	info, err := f.FileSystem.Stat(p)
	info.Modified = f.at
	return info, err
}

// A modification time of any year reaches `fs.metadata` exactly, past the
// years 1677 to 2262 where Go's UnixNano would wrap it; a zero time, a file
// system that records none, reads as the epoch, as std/embed's files do.
func TestFiles_AModificationTimeOfAnyYearIsExact(t *testing.T) {
	const src = `import
    std::fs
    std::instant.Instant
    std::io
end

fn main()
    io.print(fs.metadata("a.txt") |> Result.map(fn m -> Instant.as_nanos(m.modified)))
    io.print(fs.exists("a.txt"))
end
`
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC),
			"Ok(253402214400000000000)\nOk(True)\n"},
		{time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC),
			"Ok(-11676096000000000000)\nOk(True)\n"},
		{time.Time{}, "Ok(0)\nOk(True)\n"},
		{time.Unix(1_700_000_000, 5), "Ok(1700000000000000005)\nOk(True)\n"},
	} {
		files := timedFiles{vmhost.NewMemoryFiles(map[string][]byte{"a.txt": []byte("a")}), tc.at}
		p, err := vmhost.LoadSource("main", src, vmhost.WithFileSystem(files))
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatalf("%v: run: %v\n%s", tc.at, err, out.String())
		}
		if out.String() != tc.want {
			t.Errorf("%v: output\n%s\nwant\n%s", tc.at, out.String(), tc.want)
		}
	}
}
