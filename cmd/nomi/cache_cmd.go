package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/progcache"
	"github.com/nomi-language/nomi/internal/runners"
	"github.com/nomi-language/nomi/internal/stdcache"
	"github.com/nomi-language/nomi/internal/stdwasm"
)

const cacheUsage = `usage: nomi cache [clean|prune [packages|artifacts|runners|std]]

nomi cache prints each cache's directory and the size of what it holds.
nomi cache clean removes what every cache holds, and with a name, what
that cache holds. nomi cache prune removes now what each cache removes by
age on its own, what nothing has used for long enough, and with a name,
from that cache alone. Both refuse a directory a cache's environment
variable names that holds anything the cache does not write, and remove
nothing.`

// cache is one of the directories `nomi cache` lists, cleans and prunes:
// where it is, the variable that moves it, what decides whether a
// directory that variable names holds anything the cache does not write,
// and its prune by age, run now.
type cache struct {
	name    string
	env     string
	dir     func() (string, error)
	foreign func(dir string) (string, error)
	prune   func(dir string) artifactcache.Pruned
}

// caches is every cache `nomi` keeps, in the order `nomi cache` lists them.
//
// Each prune is the one the cache runs on its own, at most once a day or
// as it writes, with the same ages, run now, and it marks the daily one
// done. It needs no lock to leave alone what a running `nomi` is using: a
// process marks each entry it uses, by writing it or touching it
// (artifactcache.Touch, at most once an hour; a runner build's
// origin.json on every use), and every age a prune removes by is an hour
// or more past the last mark: an hour for a temporary entry being written
// and for a runner build eviction picks (its grace window), a day for the
// standard library's artifacts, and five days or two weeks for the rest
// (the artifactcache package doc, "Pruning").
var caches = []cache{
	{"packages", pkgcache.CacheEnv, pkgcache.Root, pkgcache.Foreign,
		func(dir string) artifactcache.Pruned { return pkgcache.Prune(dir, true) }},
	{"artifacts", artifactcache.RootEnv, func() (string, error) { return artifactcache.Root(), nil }, artifactcache.Foreign,
		func(dir string) artifactcache.Pruned {
			pruned := artifactcache.Prune(filepath.Join(dir, artifactcache.StdDir), "", artifactcache.StdUnused)
			pruned.Add(progcache.Prune(dir))
			pruned.Add(stdwasm.Prune(dir))
			return pruned
		}},
	{"runners", runners.CacheRootEnv, runners.CacheRoot, runners.Foreign, runners.Prune},
	{"std", stdcache.RootEnv, func() (string, error) { return stdcache.DefaultRoot(), nil }, stdcache.Foreign,
		func(dir string) artifactcache.Pruned { return stdcache.Prune(dir, "") }},
}

// runCache is `nomi cache`, `nomi cache clean [name]` and `nomi cache
// prune [name]`.
func runCache(w io.Writer, args []string) error {
	switch {
	case len(args) == 0:
		return listCaches(w)
	case (args[0] == "clean" || args[0] == "prune") && len(args) <= 2:
		chosen := caches
		if len(args) == 2 {
			chosen = nil
			for _, c := range caches {
				if c.name == args[1] {
					chosen = []cache{c}
				}
			}
			if chosen == nil {
				return fmt.Errorf("nomi cache %s: no cache is named %q; the caches are packages, artifacts, runners and std", args[0], args[1])
			}
		}
		if args[0] == "prune" {
			return pruneCaches(w, chosen)
		}
		return cleanCaches(w, chosen)
	}
	return errors.New(cacheUsage)
}

// listCaches prints each cache's directory and the size of its files.
func listCaches(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range caches {
		dir, err := c.dir()
		if err != nil {
			return fmt.Errorf("nomi cache: %s: %w", c.name, err)
		}
		if dir == "" {
			fmt.Fprintf(tw, "%s\toff (%s=off)\n", c.name, c.env)
			continue
		}
		size, err := artifactcache.TreeSize(dir)
		if err != nil {
			return fmt.Errorf("nomi cache: %s: %w", c.name, err)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.name, dir, sizeOrEmpty(size))
	}
	return tw.Flush()
}

// cacheDirs answers the directory of each of chosen for `nomi cache verb`,
// "" for one turned off. Each directory a variable names is checked first,
// and one that holds an entry its cache does not write stops the command
// before anything is removed.
func cacheDirs(verb string, chosen []cache) ([]string, error) {
	dirs := make([]string, len(chosen))
	for i, c := range chosen {
		dir, err := c.dir()
		if err != nil {
			return nil, fmt.Errorf("nomi cache %s: %s: %w", verb, c.name, err)
		}
		dirs[i] = dir
		if dir == "" || os.Getenv(c.env) == "" {
			continue
		}
		foreign, err := c.foreign(dir)
		if err != nil {
			return nil, fmt.Errorf("nomi cache %s: %s: %w", verb, c.name, err)
		}
		if foreign != "" {
			return nil, fmt.Errorf("nomi cache %s: %s holds %s, which the %s cache does not write, so %s may name a directory "+
				"that is not the cache. Nothing was removed", verb, dir, foreign, c.name, c.env)
		}
	}
	return dirs, nil
}

// cleanCaches removes what each of chosen holds, keeping each cache's
// directory.
func cleanCaches(w io.Writer, chosen []cache) error {
	dirs, err := cacheDirs("clean", chosen)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	var errs []error
	for i, c := range chosen {
		dir := dirs[i]
		if dir == "" {
			fmt.Fprintf(tw, "%s\toff (%s=off)\n", c.name, c.env)
			continue
		}
		size, err := artifactcache.TreeSize(dir)
		if err == nil {
			err = removeEntries(dir)
		}
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("nomi cache clean: %s: %w", c.name, err))
			fmt.Fprintf(tw, "%s\t%s\tnot all removed\n", c.name, dir)
		case size == 0:
			fmt.Fprintf(tw, "%s\t%s\tempty\n", c.name, dir)
		default:
			fmt.Fprintf(tw, "%s\t%s\tremoved %s\n", c.name, dir, hoverdoc.ByteSize(size))
		}
	}
	if err := tw.Flush(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// pruneCaches runs the prune of each of chosen now and prints what it
// removed.
func pruneCaches(w io.Writer, chosen []cache) error {
	dirs, err := cacheDirs("prune", chosen)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, c := range chosen {
		dir := dirs[i]
		if dir == "" {
			fmt.Fprintf(tw, "%s\toff (%s=off)\n", c.name, c.env)
			continue
		}
		switch pruned := c.prune(dir); pruned.Entries {
		case 0:
			fmt.Fprintf(tw, "%s\t%s\tnothing old enough\n", c.name, dir)
		case 1:
			fmt.Fprintf(tw, "%s\t%s\tremoved 1 entry, %s\n", c.name, dir, hoverdoc.ByteSize(pruned.Size))
		default:
			fmt.Fprintf(tw, "%s\t%s\tremoved %d entries, %s\n", c.name, dir, pruned.Entries, hoverdoc.ByteSize(pruned.Size))
		}
	}
	return tw.Flush()
}

// removeEntries removes everything dir holds, read-only trees included,
// and keeps dir. A dir that does not exist holds nothing.
func removeEntries(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if err := artifactcache.RemoveTree(filepath.Join(dir, e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sizeOrEmpty is size for a reader, or `empty` for none.
func sizeOrEmpty(size int64) string {
	if size == 0 {
		return "empty"
	}
	return hoverdoc.ByteSize(size)
}
