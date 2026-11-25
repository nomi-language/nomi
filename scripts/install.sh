#!/bin/sh
#
# Install `nomi` from a release:
#
#	curl -fsSL https://nomi-lang.org/install.sh | sh
#
# The site serves this file: scripts/build-tour-wasm.sh copies it to
# tour/public/install.sh, which is not committed, so this file is the one
# source.
#
# It downloads the release archive for this machine (macOS or Linux, amd64
# or arm64), checks its SHA-256 against the checksums file the release
# publishes, and puts `nomi` in ~/.nomi/bin. It needs curl, tar, and
# sha256sum or shasum. It never needs sudo. When the directory is not on
# PATH, it adds one line that puts it there to the profile of the user's
# shell (nomi_add_to_path), and writes nothing else outside the directory.
# Run again, it replaces `nomi` with a rename, so a `nomi` running at the
# time keeps its file, and adds no second line to a profile. It
# prints the new `nomi --version`, which names each editor whose Nomi
# support is from another version. Outside replace mode it rewrites no
# editor's files.
#
# When the PATH a new shell has finds another `nomi` before this one, it
# says which file that is, what it is and what removes it (nomi_others).
# Replace mode, NOMI_REPLACE=1, moves a machine with a build from source
# onto the release: after the install it removes each other `nomi` on PATH
# that is a build from source, leaves and names any other, and has the new
# `nomi` write the Nomi support of each editor that has any.
#
# scripts/install.ps1 installs on Windows and takes the same variables.
#
# Variables:
#
#	NOMI_VERSION           the release to install, such as v0.0.1; the
#	                       latest release when unset
#	NOMI_INSTALL_DIR       the directory `nomi` goes in, ~/.nomi/bin when unset
#	NOMI_NO_MODIFY_PATH    1 changes no profile and prints the line to add
#	NOMI_REPLACE           1 is replace mode: it removes each other nomi on
#	                       PATH built from source and sets up the editors
#	                       from this one
#	NOMI_INSTALL_BASE_URL  the releases root, in place of
#	                       https://github.com/nomi-language/nomi/releases;
#	                       for tests
#	NOMI_INSTALL_OS        stands in for `uname -s`; for tests
#	NOMI_INSTALL_ARCH      stands in for `uname -m`; for tests
#
# The whole script is one function, called on the last line, so a download
# cut short runs nothing.

nomi_install() {
	set -eu
	install_page="https://nomi-lang.org/tooling/install/"
	base="${NOMI_INSTALL_BASE_URL:-https://github.com/nomi-language/nomi/releases}"
	base="${base%/}"

	os="${NOMI_INSTALL_OS:-$(uname -s)}"
	arch="${NOMI_INSTALL_ARCH:-$(uname -m)}"
	case "$os" in
	Darwin) goos=darwin ;;
	Linux) goos=linux ;;
	MINGW* | MSYS* | CYGWIN* | Windows*)
		nomi_fail "this script installs on macOS and Linux, and found $os. On Windows, run in PowerShell: irm https://nomi-lang.org/install.ps1 | iex"
		;;
	*) nomi_fail "there is no release of nomi for the operating system $os. $install_page lists the other ways to install it" ;;
	esac
	case "$arch" in
	x86_64 | amd64)
		goarch=amd64
		# A shell under Rosetta reports x86_64 on an Apple silicon Mac,
		# which runs the arm64 build natively.
		if [ -z "${NOMI_INSTALL_ARCH:-}" ] && [ "$goos" = darwin ] &&
			[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
			goarch=arm64
		fi
		;;
	arm64 | aarch64) goarch=arm64 ;;
	*) nomi_fail "there is no release of nomi for the architecture $arch. $install_page lists the other ways to install it" ;;
	esac

	dir="${NOMI_INSTALL_DIR:-}"
	if [ -z "$dir" ]; then
		if [ -z "${HOME:-}" ]; then
			nomi_fail "HOME is not set; set NOMI_INSTALL_DIR to the directory to install nomi in"
		fi
		dir="$HOME/.nomi/bin"
	fi

	command -v curl >/dev/null 2>&1 || nomi_fail "needs curl to download the release"
	command -v tar >/dev/null 2>&1 || nomi_fail "needs tar to unpack the release"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256="sha256sum"
	elif command -v shasum >/dev/null 2>&1; then
		sha256="shasum -a 256"
	else
		nomi_fail "needs sha256sum or shasum to check the download; install one and run this again"
	fi

	version="${NOMI_VERSION:-}"
	if [ -z "$version" ]; then
		# The latest release answers with a redirect to its tag's page,
		# which names the version without a call to GitHub's API.
		location="$(curl -fsS -o /dev/null -w '%{redirect_url}' "$base/latest")" ||
			nomi_fail "could not find the latest release at $base/latest"
		case "$location" in
		*/tag/?*) version="${location##*/tag/}" ;;
		*) nomi_fail "found no release at $base/latest" ;;
		esac
	fi
	case "$version" in
	[0-9]*) version="v$version" ;;
	esac
	case "$version" in
	v[0-9]*) ;;
	*) nomi_fail "$version is not a release version such as v0.0.1" ;;
	esac
	case "$version" in
	*[!0-9A-Za-z.+-]*) nomi_fail "$version is not a release version such as v0.0.1" ;;
	esac

	archive="nomi_${version}_${goos}_${goarch}.tar.gz"
	sums="nomi_${version}_checksums.txt"

	mkdir -p "$dir" || nomi_fail "could not create $dir"
	# The download goes in the install directory, so nothing is written
	# outside it and the last step is a rename within one file system.
	work="$(mktemp -d "$dir/.nomi-install.XXXXXX")" || nomi_fail "could not create a directory in $dir"
	trap 'rm -rf "$work"' EXIT
	trap 'exit 1' HUP INT TERM

	echo "Downloading nomi $version for $goos/$goarch"
	curl -fsSL -o "$work/$archive" "$base/download/$version/$archive" ||
		nomi_fail "could not download $base/download/$version/$archive"
	curl -fsSL -o "$work/$sums" "$base/download/$version/$sums" ||
		nomi_fail "could not download $base/download/$version/$sums"

	want="$(awk -v name="$archive" '{ f = $2; sub(/^\*/, "", f); sub(/^\.\//, "", f); if (f == name) { print $1; exit } }' "$work/$sums")"
	if [ -z "$want" ]; then
		nomi_fail "$sums lists no $archive; nothing was installed"
	fi
	got="$(cd "$work" && $sha256 "$archive" | awk '{ print $1 }')"
	if [ "$got" != "$want" ]; then
		nomi_fail "the SHA-256 of $archive is $got, and $sums says $want; nothing was installed"
	fi

	mkdir "$work/unpack"
	tar -xzf "$work/$archive" -C "$work/unpack" || nomi_fail "could not unpack $archive"
	if [ ! -f "$work/unpack/nomi" ]; then
		nomi_fail "$archive holds no nomi; nothing was installed"
	fi
	chmod 755 "$work/unpack/nomi"
	mv -f "$work/unpack/nomi" "$dir/nomi" || nomi_fail "could not move nomi into $dir"

	# `nomi --version` prints its line, then one per editor whose Nomi
	# support is from another version, with the command that rewrites it.
	about="$("$dir/nomi" --version 2>/dev/null </dev/null)" || about="nomi $version"
	echo "Installed $(printf '%s\n' "$about" | sed -n 1p) at $dir/nomi"
	printf '%s\n' "$about" | sed 1d

	# Replace mode sets up the editors itself, so it names no command to.
	replace=""
	if [ "${NOMI_REPLACE:-}" = 1 ]; then
		replace=1
	fi
	next="nomi editor install <editor>    # helix, nvim, vscode or zed"
	# search is the PATH a new shell has: this one's, with the directory
	# first when it is not on it, where the profile's line puts it.
	search="${PATH:-}"
	case ":$search:" in
	*":$dir:"*)
		if [ -z "$replace" ]; then
			echo
			echo "Next, set up your editor:"
			echo "  $next"
		fi
		;;
	*)
		search="$dir${search:+:$search}"
		shown="$dir"
		case "$dir" in
		"${HOME:-}"/*) [ -n "${HOME:-}" ] && shown="\$HOME/${dir#"$HOME"/}" ;;
		esac
		if nomi_add_to_path "$dir" "$shown" "$goos"; then
			# This shell keeps the PATH it started with.
			echo
			if [ -n "$replace" ]; then
				echo "Open a new terminal to have nomi on your PATH."
			else
				echo "Open a new terminal to have nomi on your PATH. In this one, set up your editor with:"
				echo "  $dir/$next"
			fi
		else
			echo
			echo "$dir is not on your PATH. Add this line to your shell's profile, and run it in this shell:"
			echo "  export PATH=\"$shown:\$PATH\""
			if [ -z "$replace" ]; then
				echo
				echo "Then set up your editor:"
				echo "  $next"
			fi
		fi
		;;
	esac
	nomi_others "$dir" "$search" "$replace"
}

# nomi_others looks at each `nomi` the PATH list $2 finds other than the one
# installed in the directory $1, a file once however many entries reach it,
# its links followed. It looks only at a file named nomi in a directory of
# the list.
#
# With $3 empty it changes nothing: when the first nomi on the list is
# another one, it says which file that is, what it is (nomi_kind) and what
# removes it.
#
# With $3 set, replace mode, it removes each one that is a build from source
# and says so, and names each one it leaves: one Homebrew installed, a
# release, and one it cannot identify. Then the installed nomi writes the
# Nomi support of each editor that has any (`nomi editor install
# --installed --force`). A file it cannot remove, and an editor command that
# fails, are each an error line, and the script goes on and exits 1.
nomi_others() {
	nomi_ours="$(nomi_resolve "$1/nomi")"
	nomi_seen=":"
	nomi_failed=""
	nomi_said=""
	nomi_rest="$2:"
	while [ -n "$nomi_rest" ]; do
		nomi_entry="${nomi_rest%%:*}"
		nomi_rest="${nomi_rest#*:}"
		nomi_file="$nomi_entry/nomi"
		case "$nomi_entry" in
		/*) ;;
		*) continue ;;
		esac
		if [ ! -f "$nomi_file" ] || [ ! -x "$nomi_file" ]; then
			continue
		fi
		nomi_real="$(nomi_resolve "$nomi_file")"
		case "$nomi_seen" in
		*":$nomi_real:"*) continue ;;
		esac
		nomi_seen="$nomi_seen$nomi_real:"
		if [ "$nomi_real" = "$nomi_ours" ]; then
			if [ -n "$3" ]; then
				continue
			fi
			return 0
		fi
		nomi_kind "$nomi_file" "$nomi_real"
		if [ -z "$3" ]; then
			echo
			echo "Your PATH finds another nomi first: $nomi_file, $nomi_what."
			case "$nomi_is" in
			source) echo "To remove it and move your editors to this release, run: curl -fsSL https://nomi-lang.org/install.sh | NOMI_REPLACE=1 sh" ;;
			homebrew) echo "$nomi_removal" ;;
			*) echo "Remove it yourself, or put $1 ahead of it on your PATH." ;;
			esac
			return 0
		fi
		if [ -z "$nomi_said" ]; then
			echo
			nomi_said=1
		fi
		case "$nomi_is" in
		source)
			if rm -f "$nomi_file" 2>/dev/null; then
				echo "Removed $nomi_file, $nomi_what"
			else
				echo "install.sh: could not remove $nomi_file, $nomi_what" >&2
				nomi_failed=1
			fi
			;;
		*) echo "Left $nomi_file, $nomi_what. $nomi_removal" ;;
		esac
	done
	if [ -z "$3" ]; then
		return 0
	fi
	echo
	if ! "$1/nomi" editor install --installed --force </dev/null; then
		echo "install.sh: $1/nomi editor install --installed --force failed. Set up each editor with: $1/nomi editor install <editor>" >&2
		nomi_failed=1
	fi
	echo
	echo "A clone of the repository that an old nomi was built in can be deleted by hand. Nomi's caches clear themselves."
	echo "If Neovim loads Nomi's plugin through a plugin manager, remove that entry from your Neovim configuration and run: nomi editor install nvim"
	if [ -n "$nomi_failed" ]; then
		exit 1
	fi
}

# nomi_kind says what the nomi at $1 is, $2 being the file with its links
# followed: in nomi_is, in words in nomi_what, and what removes one this
# script leaves in nomi_removal:
#
#	homebrew  under a Homebrew Cellar
#	release   its `--version` names a release and the commit it was cut
#	          from, as a release archive's nomi does
#	source    any other `nomi --version` line: built in a clone, which
#	          names a commit alone, or with `go install ...@version`, which
#	          names a version alone
#	unknown   it does not run, or prints something else
nomi_kind() {
	nomi_removal="Remove it yourself if it is not wanted."
	case "$2" in
	*/Cellar/*)
		nomi_is=homebrew
		nomi_what="installed by Homebrew"
		nomi_removal="To remove it, run: brew uninstall nomi"
		return 0
		;;
	esac
	nomi_line="$("$1" --version 2>/dev/null </dev/null | sed -n 1p)" || nomi_line=""
	case "$nomi_line" in
	"nomi v"[0-9]*" ("*", "*")")
		nomi_is=release
		nomi_what="a release installed another way ($nomi_line)"
		;;
	"nomi "?*" ("*")")
		nomi_is=source
		nomi_what="a build from source ($nomi_line)"
		;;
	*)
		nomi_is=unknown
		nomi_what="which this script cannot identify"
		;;
	esac
}

# nomi_resolve prints the file $1 with the links in its path followed.
nomi_resolve() {
	nomi_path="$1"
	nomi_hops=0
	while [ -L "$nomi_path" ] && [ "$nomi_hops" -lt 40 ]; do
		nomi_target="$(readlink "$nomi_path" 2>/dev/null)" || break
		case "$nomi_target" in
		/*) nomi_path="$nomi_target" ;;
		*) nomi_path="$(dirname "$nomi_path")/$nomi_target" ;;
		esac
		nomi_hops=$((nomi_hops + 1))
	done
	if nomi_parent="$(cd -P "$(dirname "$nomi_path")" 2>/dev/null && pwd -P)"; then
		nomi_path="${nomi_parent%/}/$(basename "$nomi_path")"
	fi
	printf '%s\n' "$nomi_path"
}

# nomi_add_to_path puts the directory $1, spelled $2 in a profile, on the
# PATH of every new shell of the user's ($SHELL), as rustup and Bun do: one
# line in zsh's .zshrc; in bash's .bashrc, and on macOS ($3 darwin) also in
# the profile a login shell reads; for fish a file in its conf.d. A profile
# that holds the line already is left as it is, so a rerun adds nothing,
# and the line it writes is named. It fails, changing nothing, under
# NOMI_NO_MODIFY_PATH=1, for any other shell, with no HOME, and for a
# directory whose name a profile would have to escape.
nomi_add_to_path() {
	[ "${NOMI_NO_MODIFY_PATH:-}" = 1 ] && return 1
	[ -n "${HOME:-}" ] || return 1
	case "$1" in
	*[\"\\\$\`]*) return 1 ;;
	esac
	nomi_line="export PATH=\"$2:\$PATH\""
	nomi_changed=""
	case "${SHELL:-}" in
	zsh | */zsh) nomi_profile "${ZDOTDIR:-$HOME}/.zshrc" "$nomi_line" || return 1 ;;
	bash | */bash)
		nomi_profile "$HOME/.bashrc" "$nomi_line" || return 1
		if [ "$3" = darwin ]; then
			# Terminal starts bash as a login shell, which reads the first of
			# these it finds and not .bashrc.
			if [ ! -f "$HOME/.bash_profile" ] && [ -f "$HOME/.profile" ]; then
				nomi_profile "$HOME/.profile" "$nomi_line" || return 1
			else
				nomi_profile "$HOME/.bash_profile" "$nomi_line" || return 1
			fi
		fi
		;;
	fish | */fish)
		nomi_profile "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/nomi.fish" "fish_add_path --path \"$2\"" || return 1
		;;
	*) return 1 ;;
	esac
	if [ -n "$nomi_changed" ]; then
		echo "Added $1 to PATH in $nomi_changed"
	fi
}

# nomi_profile appends the line $2 to the file $1, making it and its
# directory if need be, unless a line of the file is $2 already, and adds
# the file's name to nomi_changed when it wrote it.
nomi_profile() {
	if [ -f "$1" ] && grep -qxF "$2" "$1"; then
		return 0
	fi
	mkdir -p "$(dirname "$1")" 2>/dev/null || return 1
	printf '\n# nomi\n%s\n' "$2" >>"$1" 2>/dev/null || return 1
	nomi_file="$1"
	case "$1" in
	"$HOME"/*) nomi_file="~/${1#"$HOME"/}" ;;
	esac
	nomi_changed="$nomi_changed${nomi_changed:+ and }$nomi_file"
}

nomi_fail() {
	echo "install.sh: $1" >&2
	exit 1
}

nomi_install "$@"
