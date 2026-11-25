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
# sha256sum or shasum. It never needs sudo, writes nothing outside the
# install directory and changes no shell profile. Run again, it replaces
# `nomi` with a rename, so a `nomi` running at the time keeps its file.
# Windows has no script: its release is a zip (docs/install.md).
#
# Variables:
#
#	NOMI_VERSION           the release to install, such as v0.0.1; the
#	                       latest release when unset
#	NOMI_INSTALL_DIR       the directory `nomi` goes in, ~/.nomi/bin when unset
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
		nomi_fail "this script installs on macOS and Linux, and found $os. On Windows, download the zip from the release page: $install_page"
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

	echo "Installed $("$dir/nomi" --version 2>/dev/null || echo "nomi $version") at $dir/nomi"
	case ":${PATH:-}:" in
	*":$dir:"*)
		echo
		echo "Next, set up your editor:"
		echo "  nomi editor install <editor>    # helix, nvim, vscode or zed"
		;;
	*)
		shown="$dir"
		case "$dir" in
		"${HOME:-}"/*) [ -n "${HOME:-}" ] && shown="\$HOME/${dir#"$HOME"/}" ;;
		esac
		echo
		echo "$dir is not on your PATH. Add this line to your shell's profile, and run it in this shell:"
		echo "  export PATH=\"$shown:\$PATH\""
		echo
		echo "Then set up your editor:"
		echo "  nomi editor install <editor>    # helix, nvim, vscode or zed"
		;;
	esac
}

nomi_fail() {
	echo "install.sh: $1" >&2
	exit 1
}

nomi_install "$@"
