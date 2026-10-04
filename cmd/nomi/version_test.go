package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// TestVersionToken pins the FORMAT, in isolation from any build.
//
// The clean/dirty pair is the reason this table exists rather than only the
// end-to-end test below: an implementation that read `vcs.revision` and
// ignored `vcs.modified` would pass every assertion that only looks for the
// revision, and the tree is dirty during development and clean in a release,
// so the two states are exactly the ones a user needs told apart.
func TestVersionToken(t *testing.T) {
	const rev = "e8635380091fdebf6ee103ebbd8a47a70eeb0fea"
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{{
		name: "no build info at all",
		info: nil,
		want: "unknown",
	}, {
		// A checkout build: Go derives v1.4.0+dirty or a pseudo-version from the
		// nearest tag, and the commit says more than that does.
		name: "a revision wins over the module version a checkout derives",
		info: &debug.BuildInfo{
			Main:     debug.Module{Version: "v1.4.0+dirty"},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}},
		},
		want: "e8635380091f",
	}, {
		// `go install github.com/nomi-language/nomi/cmd/nomi@v1.4.0` builds from
		// the module proxy, which carries a version and no VCS information.
		name: "a module version with no revision is the version",
		info: &debug.BuildInfo{Main: debug.Module{Version: "v1.4.0"}},
		want: "v1.4.0",
	}, {
		name: "devel plus a clean tree is the bare revision",
		info: &debug.BuildInfo{
			Main: debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: rev},
				{Key: "vcs.modified", Value: "false"},
			},
		},
		want: "e8635380091f",
	}, {
		name: "devel plus a dirty tree carries git's own marker",
		info: &debug.BuildInfo{
			Main: debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: rev},
				{Key: "vcs.modified", Value: "true"},
			},
		},
		want: "e8635380091f-dirty",
	}, {
		// -buildvcs=false, or a build from an unpacked source archive. There is
		// nothing to report and the token says so instead of coming out empty.
		name: "devel with no vcs information is named",
		info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
		want: "unknown",
	}, {
		// A revision shorter than the abbreviation is passed through rather
		// than indexed past its end.
		name: "a revision shorter than the abbreviation survives",
		info: &debug.BuildInfo{
			Main:     debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc1234"}},
		},
		want: "abc1234",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := versionToken(c.info); got != c.want {
				t.Fatalf("versionToken = %q, want %q", got, c.want)
			}
		})
	}
}

// TestVersionLineFor_ARelease names its tag and keeps its revision.
func TestVersionLineFor_ARelease(t *testing.T) {
	info := &debug.BuildInfo{
		Main:     debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "e8635380091fdebf6ee103ebbd8a47a70eeb0fea"}},
	}
	got := versionLineFor("v0.1.0", info)
	if !strings.HasPrefix(got, "nomi v0.1.0 (e8635380091f, go") {
		t.Fatalf("a release's version line is %q", got)
	}
	if got := versionLineFor("", info); !strings.HasPrefix(got, "nomi e8635380091f (go") {
		t.Fatalf("a non-release version line is %q", got)
	}
}

func TestVersionRequested(t *testing.T) {
	yes := [][]string{{"nomi", "--version"}, {"nomi", "version"}}
	no := [][]string{{"nomi"}, {"nomi", "run", "x.nomi"}, {"nomi", "-version"}, {"nomi", "--versions"}, {"nomi", "run", "--version"}}
	for _, args := range yes {
		if !versionRequested(args) {
			t.Errorf("versionRequested(%q) = false, want true", args)
		}
	}
	for _, args := range no {
		if versionRequested(args) {
			t.Errorf("versionRequested(%q) = true, want false", args)
		}
	}
}

// TestVersionFlag_EveryBuildPathReportsItsRevision is the acceptance test, and
// the reason it builds four binaries is that this repository produces `nomi`
// three different ways and the fourth is the negative control.
//
//	go build -o nomi ./cmd/nomi          Makefile's build-cli, first line
//	go install ./cmd/nomi                Makefile's build-cli, second line
//	-trimpath -ldflags "-s -w"               scripts/release.sh's exact flags
//	-buildvcs=false                          nothing stamped: reports `unknown`
//
// The release shape is here because `-s -w` strips the symbol table and DWARF,
// and a version read out of the binary is exactly the kind of thing that would
// plausibly go with them. It does not: build info lives in its own section.
//
// EVERY ARM COMPUTES ITS OWN EXPECTATION from `go version -m`, the toolchain's
// own reader of the artifact's build-info section, so a `versionToken` that
// returned a constant fails all four. The 40-hex-digit requirement on the
// parsed revision is what keeps that honest: without it a parser that matched
// nothing would hand back the empty string, every assertion would hold
// vacuously, and the negative control would be indistinguishable from the
// other three.
func TestVersionFlag_EveryBuildPathReportsItsRevision(t *testing.T) {
	root := repoRoot(t)
	recipes := []struct {
		name string
		// build produces a `nomi` in dir and returns its path.
		build func(t *testing.T, dir string) string
		// stamped is false for the arm built with no VCS information.
		stamped bool
	}{{
		name:    "go build -o",
		stamped: true,
		build: func(t *testing.T, dir string) string {
			bin := filepath.Join(dir, "nomi")
			mustRunGo(t, root, nil, "build", "-o", bin, "./cmd/nomi")
			return bin
		},
	}, {
		name:    "go install",
		stamped: true,
		build: func(t *testing.T, dir string) string {
			mustRunGo(t, root, []string{"GOBIN=" + dir}, "install", "./cmd/nomi")
			return filepath.Join(dir, "nomi")
		},
	}, {
		name:    "release shape: -trimpath -ldflags -s -w",
		stamped: true,
		build: func(t *testing.T, dir string) string {
			bin := filepath.Join(dir, "nomi")
			mustRunGo(t, root, nil, "build", "-trimpath", "-ldflags", "-s -w", "-o", bin, "./cmd/nomi")
			return bin
		},
	}, {
		name:    "no vcs information",
		stamped: false,
		build: func(t *testing.T, dir string) string {
			bin := filepath.Join(dir, "nomi")
			mustRunGo(t, root, nil, "build", "-buildvcs=false", "-o", bin, "./cmd/nomi")
			return bin
		},
	}}

	for _, r := range recipes {
		t.Run(r.name, func(t *testing.T) {
			bin := r.build(t, t.TempDir())
			revision, modified := stampedVCS(t, bin)
			want := "unknown"
			if r.stamped {
				if len(revision) != 40 {
					t.Fatalf("`go version -m %s` reports vcs.revision %q, want 40 hex digits; "+
						"without a real revision here every assertion below is vacuous", bin, revision)
				}
				want = revision[:revisionAbbrev]
				if modified {
					want += "-dirty"
				}
			} else if revision != "" {
				t.Fatalf("-buildvcs=false stamped vcs.revision %q; this arm is the negative "+
					"control and has nothing left to control for", revision)
			}

			for _, args := range [][]string{{"--version"}, {"version"}} {
				out, err := runBinary(bin, args...)
				if err != nil {
					t.Fatalf("nomi %v: %v\n%s", args, err, out)
				}
				line := strings.TrimSuffix(out, "\n")
				if strings.Contains(line, "\n") {
					t.Fatalf("nomi %v printed more than one line:\n%s", args, out)
				}
				fields := strings.Fields(line)
				if len(fields) < 2 || fields[0] != "nomi" {
					t.Fatalf("nomi %v printed %q, want a line starting `nomi <version>`", args, line)
				}
				if fields[1] != want {
					t.Fatalf("nomi %v reported version %q, want %q (from `go version -m`)", args, fields[1], want)
				}
			}
		})
	}
}

// stampedVCS reads the VCS revision and dirtiness out of a binary's build-info
// section with `go version -m` — the toolchain's own reader, independent of the
// debug.ReadBuildInfo call under test.
//
// A `build` row is TWO tab-separated fields, `build` and `key=value`, unlike a
// `dep` row's three. scripts/release.sh:106 reads GOOS the same way. Written
// with three fields first, this parser matched nothing, returned the empty
// revision, and made every containment assertion hold for a binary that was in
// fact stamped correctly — which is what the 40-digit guard below exists for.
func stampedVCS(t *testing.T, bin string) (revision string, modified bool) {
	t.Helper()
	out, err := exec.Command("go", "version", "-m", bin).Output()
	if err != nil {
		t.Fatalf("go version -m %s: %v", bin, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "build" {
			continue
		}
		key, value, ok := strings.Cut(fields[1], "=")
		if !ok {
			continue
		}
		switch key {
		case "vcs.revision":
			revision = value
		case "vcs.modified":
			modified = value == "true"
		}
	}
	return revision, modified
}

// mustRunGo runs the Go toolchain in dir with extra environment entries.
func mustRunGo(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	// -C must come first: `go build -o x -C dir .` exits 2 with no output.
	cmd := exec.Command("go", append([]string{args[0], "-C", dir}, args[1:]...)...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", cmd.Args[1:], err, out)
	}
}
