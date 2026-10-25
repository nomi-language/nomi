package collation_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/collation"
)

// The root collation a program carries passes CLDR's conformance tests for
// its release (common/uca/CollationTest_CLDR_*_SHORT.txt, vendored gzipped
// in testdata): every line sorts at or after the line before it, at the
// identical level, with variable characters non-ignorable in one file and
// shifted in the other. Lines with lone surrogates, which a Nomi string
// cannot hold, are skipped. The sort keys order the lines the same way.
func TestRootConformanceNonIgnorable(t *testing.T) {
	conformance(t, "CollationTest_CLDR_NON_IGNORABLE_SHORT.txt.gz", false)
}

func TestRootConformanceShifted(t *testing.T) {
	conformance(t, "CollationTest_CLDR_SHIFTED_SHORT.txt.gz", true)
}

func shippedRoot(t *testing.T) *collation.Root {
	t.Helper()
	root, err := collation.DecodeRoot(cldrfull.Full().CollationRoot())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func conformance(t *testing.T, file string, shifted bool) {
	f, err := os.Open(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	c := collation.New(shippedRoot(t), nil, func(s *collation.Settings) {
		s.Strength = collation.Identical
		s.Shifted = shifted
	})
	sc := bufio.NewScanner(z)
	var prev, prevLine string
	var prevKey []byte
	lines, failed, surrogates, n := 0, 0, 0, 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var b strings.Builder
		sur := false
		for _, h := range strings.Fields(line) {
			v, err := strconv.ParseUint(h, 16, 32)
			if err != nil {
				t.Fatalf("line %d: %v", n, err)
			}
			if v >= 0xd800 && v <= 0xdfff {
				sur = true
			}
			b.WriteRune(rune(v))
		}
		if sur {
			surrogates++
			continue
		}
		s := b.String()
		key := c.SortKey(s)
		lines++
		if lines > 1 && (c.Compare(prev, s) > 0 || bytes.Compare(prevKey, key) > 0) {
			failed++
			if failed <= 20 {
				t.Errorf("line %d: %s sorts after the next line %s", n, prevLine, line)
			}
		}
		prev, prevLine, prevKey = s, line, key
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d of %d lines in order (%d lines with lone surrogates skipped)", file, lines-failed, lines, surrogates)
}
