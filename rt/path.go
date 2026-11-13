package rt

import "path"

// The Go side of std/path/host: Go's path package over a path's text.
// std::path builds the rest of its functions over these.

// PathClean is `std::path::host.clean`: Go's path.Clean.
func PathClean(p string) string { return path.Clean(p) }

// PathJoin is `std::path::host.join`: Go's path.Join, which leaves out the empty
// parts, joins the rest with `/` and cleans the result, and answers "" when
// every part is empty.
func PathJoin(parts *List[string]) string {
	var elems []string
	for l := parts; l != nil; l = l.Tail {
		elems = append(elems, l.Head)
	}
	return path.Join(elems...)
}

// PathBase is `std::path::host.base`: Go's path.Base of the cleaned path, so
// "a/.." is "." rather than "..".
func PathBase(p string) string { return path.Base(path.Clean(p)) }

// PathDir is `std::path::host.dir`: Go's path.Dir.
func PathDir(p string) string { return path.Dir(p) }
