#!/bin/sh
# Rebuilds sqlite3.wasm, the SQLite the package sqlite embeds, from
# SQLite's amalgamation and shim.c, with the pinned zig, and writes and
# prints its SHA-256, which sqlite3.wasm.sha256 records: a test in
# internal/engine_test.nomi holds the committed module to it. The same
# amalgamation and zig build the same bytes.
#
#   engine/build.sh                  # fetches the pinned amalgamation
#   engine/build.sh <dir>            # an unpacked amalgamation, the pinned release
#
# Compile options, besides what each says in place:
#   SQLITE_OS_OTHER=1            no operating system layer of SQLite's own:
#                                shim.c's, over the module's imports
#   SQLITE_THREADSAFE=0          one call runs at a time on an instance
#   SQLITE_OMIT_LOAD_EXTENSION   a module cannot load native code
#   SQLITE_DEFAULT_MEMSTATUS=0   no memory statistics
#   HAVE_USLEEP=1                the busy handler sleeps in milliseconds, not seconds
#   SQLITE_DEFAULT_CACHE_SIZE=-16384  a 16 MiB page cache, not 2 MiB: a page
#                                the cache misses is a call out of the module
#   HAVE_LOCALTIME_R=1, localtime_r=nomi_localtime_r
#                                SQLite's local time is shim.c's, over the
#                                module's local_offset import: WASI's C
#                                library reads every zone as UTC
# and, as the Go build of SQLite this module used before enabled them:
#   SQLITE_ENABLE_COLUMN_METADATA  sqlite/describe reads each column's table
#   SQLITE_ENABLE_FTS5, SQLITE_ENABLE_RTREE, SQLITE_ENABLE_GEOPOLY,
#   SQLITE_ENABLE_DBSTAT_VTAB, SQLITE_ENABLE_MATH_FUNCTIONS,
#   SQLITE_ENABLE_STAT4, SQLITE_ENABLE_OFFSET_SQL_FUNC, SQLITE_SOUNDEX,
#   SQLITE_LIKE_DOESNT_MATCH_BLOBS, SQLITE_DEFAULT_RECURSIVE_TRIGGERS=1
# The math functions are compiled in from zig's libc, so they are no
# imports.
set -eu

version=3540100
version_number=3054001
year=2026
zip_sum=0a9c8e34352dad46006aa82836d5383bff9dc6bc9ce5a60553c05aa30e6ad1f9
zig_version=0.15.2

here=$(dirname "$0")
here=$(realpath "$here")

if [ "$(zig version)" != "$zig_version" ]; then
	echo "build.sh: zig $zig_version builds sqlite3.wasm, and this is zig $(zig version)" >&2
	exit 1
fi

src=${1:-}
if [ -z "$src" ]; then
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	curl -sSfL -o "$tmp/amalgamation.zip" "https://sqlite.org/$year/sqlite-amalgamation-$version.zip"
	got=$(shasum -a 256 "$tmp/amalgamation.zip" | cut -d' ' -f1)
	if [ "$got" != "$zip_sum" ]; then
		echo "build.sh: the amalgamation's SHA-256 is $got, not $zip_sum" >&2
		exit 1
	fi
	unzip -q "$tmp/amalgamation.zip" -d "$tmp"
	src="$tmp/sqlite-amalgamation-$version"
fi

if ! grep -q "#define SQLITE_VERSION_NUMBER $version_number" "$src/sqlite3.h"; then
	echo "build.sh: $src does not hold SQLite $version_number" >&2
	exit 1
fi

zig cc -target wasm32-wasi -mexec-model=reactor -O2 -s \
	-I"$src" \
	-DSQLITE_OS_OTHER=1 \
	-DSQLITE_THREADSAFE=0 \
	-DSQLITE_OMIT_LOAD_EXTENSION \
	-DSQLITE_DEFAULT_MEMSTATUS=0 \
	-DHAVE_USLEEP=1 \
	-DSQLITE_DEFAULT_CACHE_SIZE=-16384 \
	-DHAVE_LOCALTIME_R=1 \
	-Dlocaltime_r=nomi_localtime_r \
	-DSQLITE_ENABLE_COLUMN_METADATA \
	-DSQLITE_ENABLE_FTS5 \
	-DSQLITE_ENABLE_RTREE \
	-DSQLITE_ENABLE_GEOPOLY \
	-DSQLITE_ENABLE_DBSTAT_VTAB \
	-DSQLITE_ENABLE_MATH_FUNCTIONS \
	-DSQLITE_ENABLE_STAT4 \
	-DSQLITE_ENABLE_OFFSET_SQL_FUNC \
	-DSQLITE_SOUNDEX \
	-DSQLITE_LIKE_DOESNT_MATCH_BLOBS \
	-DSQLITE_DEFAULT_RECURSIVE_TRIGGERS=1 \
	-Wl,-z,stack-size=1048576 \
	-o "$here/sqlite3.wasm" \
	"$here/shim.c" "$src/sqlite3.c"

sum=$(shasum -a 256 "$here/sqlite3.wasm" | cut -d' ' -f1)
echo "$sum  sqlite3.wasm" > "$here/sqlite3.wasm.sha256"
echo "$sum  sqlite3.wasm"
