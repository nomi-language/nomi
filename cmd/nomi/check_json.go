package main

import (
	"io"
	"path/filepath"
	"sort"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

// runCheckJSON is runCheck in json mode, writing `nomi check --format
// json`'s records (vmhost.CheckJSON) to w. An FFI program's wrapper writes
// its own diagnostics' records, which pass through to w.
func runCheckJSON(w io.Writer, path string) error {
	cj := vmhost.NewCheckJSON(w)
	files, through, _, err := frontend.CheckedFiles(path)
	if err != nil {
		abs, _ := filepath.Abs(path)
		cj.File(abs, pathError(path, err))
		cj.Summary()
		return errCheckFailed
	}
	failed := map[string]bool{}
	for _, file := range files {
		err := checkFile(file, vmhost.WithWarningsTo(cj.Warnings), cj)
		failed[file] = err != nil
		cj.File(file, err)
	}
	helpers := make([]string, 0, len(through))
	for helper := range through {
		helpers = append(helpers, helper)
	}
	sort.Strings(helpers)
	for _, helper := range helpers {
		ok := true
		for _, importer := range through[helper] {
			ok = ok && !failed[importer]
		}
		if ok {
			cj.Through(helper, through[helper][0])
		}
	}
	cj.Summary()
	if cj.Failed() > 0 {
		return errCheckFailed
	}
	return nil
}
