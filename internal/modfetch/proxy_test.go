package modfetch

import (
	"net/url"
	"runtime"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// A file GOPROXY names a directory by a URL; on Windows the drive follows
// the URL's slash, and the path read is the drive's.
func TestFileURLPath_IsTheOperatingSystemsPath(t *testing.T) {
	for _, c := range []struct {
		url     string
		windows bool
		want    string
	}{
		{"file:///home/me/proxy/example.com/@v/list", false, "/home/me/proxy/example.com/@v/list"},
		{"file:///C:/Users/me/proxy/example.com/@v/v1.0.0.zip", true, `C:\Users\me\proxy\example.com\@v\v1.0.0.zip`},
		{"file:///d:/proxy/@latest", true, `d:\proxy\@latest`},
		{"file:///proxy/@latest", true, `\proxy\@latest`},
	} {
		u, err := url.Parse(c.url)
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != "" {
			t.Fatalf("%s: host %q", c.url, u.Host)
		}
		if got := fileURLPath(u.Path, c.windows); got != c.want {
			t.Errorf("fileURLPath(%q, windows=%v) = %q, want %q", u.Path, c.windows, got, c.want)
		}
	}
}

// The URL fileuri writes for this system's path reads back as that path.
func TestFileURLPath_ReadsWhatFileuriWrites(t *testing.T) {
	dir := t.TempDir()
	u, err := url.Parse(fileuri.FromPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "" {
		t.Fatalf("%s names host %q", u, u.Host)
	}
	if got := fileURLPath(u.Path, runtime.GOOS == "windows"); got != dir {
		t.Errorf("fileURLPath of %s = %q, want %q", u, got, dir)
	}
}
