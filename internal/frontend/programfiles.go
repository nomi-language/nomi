package frontend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The programs a path names: the files `nomi check` and `nomi effects`
// check as programs (CheckedFiles), and those `nomi test` runs
// (TestFiles). The language server reads the same set to answer what a
// project's programs reach in its nomi.toml.

// StartsWithShebang reports whether path is a regular file whose first two
// bytes are `#!`.
func StartsWithShebang(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 2)
	n, _ := io.ReadFull(f, head)
	return n == 2 && string(head) == "#!"
}

// CheckedFiles answers the files `nomi check root` checks as programs,
// and, for a directory without entry points, the files it checks only through
// those programs, each with the programs that load it (ProgramRoots).
func CheckedFiles(root string) (files []string, through map[string][]string, singleFile bool, err error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil, false, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(root, ".nomi") && !StartsWithShebang(root) {
			return nil, nil, false, fmt.Errorf("%s is not a .nomi file and does not start with a #! line", root)
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, nil, false, err
		}
		return []string{abs}, nil, true, nil
	}
	if mfst, err := analysis.LoadManifest(root); err == nil && len(mfst.Entries(root)) > 0 {
		entries := mfst.Entries(root)
		files := make([]string, 0, len(entries))
		for _, entry := range entries {
			abs, err := EntryFile(root, entry)
			if err != nil {
				return nil, nil, false, err
			}
			files = append(files, abs)
		}
		// The module's test files, and files that declare tests, are
		// checked with its entries, as `nomi test` would load them.
		tests, err := TestFiles(root)
		if err != nil {
			return nil, nil, false, err
		}
		seen := map[string]bool{}
		for _, f := range files {
			seen[f] = true
		}
		for _, f := range tests {
			if !seen[f] {
				files = append(files, f)
			}
		}
		sort.Strings(files)
		return files, nil, false, nil
	} else if err != nil && !errors.Is(err, analysis.ErrManifestMissing) {
		return nil, nil, false, err
	}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".astro":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".nomi") {
			return nil
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		files = append(files, abs)
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	sort.Strings(files)
	files, through = ProgramRoots(files)
	return files, through, false, nil
}

// EntryFile is the absolute path of the file an entry of the nomi.toml in
// root names: its .nomi file, named without the extension, or an
// extensionless `#!` script beside nomi.toml when no such .nomi file
// exists. `nomi check` and `nomi effects` check each entry's file, and
// `nomi run` and `nomi build` run or build a project's one entry point.
func EntryFile(root, entry string) (string, error) {
	entryPath := filepath.FromSlash(entry)
	if filepath.Ext(entryPath) != ".nomi" {
		bare := filepath.Join(root, entryPath)
		if _, err := os.Stat(bare + ".nomi"); err == nil || !StartsWithShebang(bare) {
			entryPath += ".nomi"
		}
	}
	return filepath.Abs(filepath.Join(root, entryPath))
}

// ProgramRoots is the files of a directory check that are checked as
// programs. A file another file in the set imports is checked through that
// importer, as `nomi run` and `nomi test` load it: a helper that reads an
// application field is valid only under an entry whose boot returns the
// application, and its errors are reported by its importer's check. A file
// nothing in the set imports is checked on its own. Files that import each
// other with no importer outside their cycle are checked from the first, in
// path order. A stdlib file is always checked on its own, as its module.
func ProgramRoots(files []string) (roots []string, through map[string][]string) {
	inSet := map[string]bool{}
	for _, f := range files {
		inSet[f] = true
	}
	imports := map[string][]string{}
	imported := map[string]bool{}
	for _, f := range files {
		if _, _, ok := StdlibFile(f); ok {
			continue
		}
		// An unreadable import graph leaves the file a root; its own check
		// reports why.
		paths, _ := ImportedFiles(f)
		for _, p := range paths {
			if inSet[p] && p != f {
				imports[f] = append(imports[f], p)
				imported[p] = true
			}
		}
	}
	// ImportedFiles is transitive, so a root's imports are every file its
	// program loads.
	isRoot := map[string]bool{}
	reached := map[string]bool{}
	addRoot := func(f string) {
		roots = append(roots, f)
		isRoot[f] = true
		reached[f] = true
		for _, p := range imports[f] {
			reached[p] = true
		}
	}
	for _, f := range files {
		if !imported[f] {
			addRoot(f)
		}
	}
	for _, f := range files {
		if !reached[f] {
			addRoot(f)
		}
	}
	sort.Strings(roots)
	through = map[string][]string{}
	for _, r := range roots {
		for _, p := range imports[r] {
			if !isRoot[p] {
				through[p] = append(through[p], r)
			}
		}
	}
	return roots, through
}

// TestFiles is the files `nomi test root` runs, sorted: root itself when
// it is a file, else each `_test.nomi` file under it and each other file
// that declares tests.
func TestFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(root, ".nomi") && !StartsWithShebang(root) {
			return nil, fmt.Errorf("%s is not a .nomi file and does not start with a #! line", root)
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		return []string{abs}, nil
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", ".astro":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".nomi") {
			return nil
		}
		hasTests := strings.HasSuffix(d.Name(), "_test.nomi")
		if !hasTests {
			var err error
			hasTests, err = FileDeclaresTests(path)
			if err != nil {
				return err
			}
		}
		if hasTests {
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			files = append(files, abs)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
