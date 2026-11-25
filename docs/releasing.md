# Releasing Nomi

This is the maintainer's checklist for cutting a release, written for
`v0.0.1`. Users install a release as `docs/install.md` says. Each step says
whether a script does it or it is done by hand.

## What a release is

A release is one git tag of this repository, `vX.Y.Z`, and a GitHub release
of that tag holding the archives `scripts/release.sh` builds:

| Archive | Holds |
|---|---|
| `nomi_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) | `nomi` alone, carrying its platform's two runners |
| `nomi_<version>_<os>_<arch>_runners.tar.gz` | `nomi-vm` and `nomi-vm-nonet`, for `nomi build --target` on another platform |
| `nomi_<version>_<os>_<arch>_compiler.tar.gz` | std::compiler's runner |
| `nomi_<version>_js_wasm_*.tar.gz`, `nomi_<version>_wasip1_wasm_*.tar.gz` | the WebAssembly runners and compiler runners |
| `nomi_<version>_checksums.txt` | the SHA-256 of every archive |

The five platforms with a `nomi` are darwin/arm64, darwin/amd64,
linux/amd64, linux/arm64 and windows/amd64; with js/wasm and wasip1/wasm
that is 19 archives. `nomi build --target` and the install script check a
download against the checksums file, and the Homebrew formula is rendered
from it.

The binaries are not signed and not notarized. Windows is exercised by CI's
Go tests only.

The version given to `scripts/release.sh` must be the release's tag. It is
stamped into `nomi`
(`-X github.com/nomi-language/nomi/internal/runners.ReleaseVersion=<version>`),
`nomi --version` prints it, and `nomi build` downloads a runner it does not
carry from the release of that tag. To try the build without publishing,
`NOMI_RELEASE_DIST=<dir>` writes the archives to another directory and
`NOMI_RELEASE_TARGETS="darwin/arm64"` builds only the targets it lists:

```console
$ NOMI_RELEASE_DIST="$(mktemp -d)" NOMI_RELEASE_TARGETS="darwin/arm64" scripts/release.sh v0.0.0-dryrun
```

The release workflow's manual run (`workflow_dispatch`) builds every target
the same way and keeps the archives as a workflow artifact.

## Versions

Versions follow semantic versioning, with one rule for `0.0.x`: every
`0.0.x` release may break the one before, as every `0.x` minor release and
every major release may. The tooling reference's "Packages and dependencies"
says how `nomi` reads versions. The one tag also versions every first-party
package under `lib/`: a release `nomi` takes them at its own version, so
`lib/` packages are never tagged apart.

## Cutting a release

1. **The release commit, on `next`, by hand.** Remove the sentences that
   say no release exists:
   - `docs/install.md`: the paragraph under the title that says the first
     release is not published yet, and in "With `go install`, without a
     clone", the sentence that says `@latest` is the newest commit on
     `main` with no tagged versions.
   - `README.md`, "Getting started": the sentence that says no release is
     published yet, and the source build it leads to, in favor of the
     install page.

   Gather the release's notes in `next-notes.txt` as for any batch.
2. **The whole-tree check.** `make check-full` on `next`, then
   `make publish-main`, which pushes `next`'s tree to `main` as one commit.
3. **The tag.** In a clean checkout of the new `main` commit:

   ```console
   $ scripts/release-tags.sh v0.0.1
   $ scripts/release-tags.sh --create v0.0.1
   $ git push origin v0.0.1
   ```

   The first command checks that every `lib/` package names itself and names
   its neighbors by `dir` alone; the second makes the annotated tag at
   HEAD; the push is by hand. The pre-push hook lets a tag through.
4. **The archives and checksums.** Pushing the tag runs
   `.github/workflows/release.yml`, which runs `scripts/release.sh v0.0.1`
   on one Linux machine, checks the checksums file against the archives and
   attaches both to a GitHub release of the tag with generated notes. Watch
   the run: `gh run watch`. When Actions cannot run it, build and upload by
   hand from a clean checkout at the tag, with Go, Node 22+ and npm, rustup
   with the `wasm32-wasip2` target, and `zip`:

   ```console
   $ scripts/release.sh v0.0.1
   $ gh release create v0.0.1 --generate-notes dist/*.tar.gz dist/*.zip dist/nomi_v0.0.1_checksums.txt
   ```

   `scripts/release.sh` refuses a binary whose platform, build tags, commit
   or module version is not the one it built, and checks that it wrote 19
   archives and 19 checksum lines.
5. **Check the release.** Install it the way a user does, into a scratch
   directory, from a checkout's script (the site may still serve the one
   before):

   ```console
   $ NOMI_INSTALL_DIR="$(mktemp -d)" sh scripts/install.sh
   ```

   It must name `v0.0.1` and run `nomi --version`.
6. **The Homebrew formula, by hand.** The tap is the repository
   `nomi-language/homebrew-nomi`, which `brew install
   nomi-language/nomi/nomi` reads; make it, public and empty, before the
   first release. Render the formula from the published checksums and push
   it as `Formula/nomi.rb`:

   ```console
   $ gh release download v0.0.1 --pattern nomi_v0.0.1_checksums.txt
   $ mkdir -p ../homebrew-nomi/Formula
   $ scripts/homebrew-formula.sh v0.0.1 nomi_v0.0.1_checksums.txt > ../homebrew-nomi/Formula/nomi.rb
   $ git -C ../homebrew-nomi add Formula/nomi.rb
   $ git -C ../homebrew-nomi commit -m "nomi v0.0.1"
   $ git -C ../homebrew-nomi push
   $ brew install nomi-language/nomi/nomi && brew test nomi
   ```

   The template is `packaging/homebrew/nomi.rb`; `scripts/release.sh` also
   renders `dist/nomi.rb` when it builds every target. Never type a
   checksum into the formula.
7. **install.sh.** The site serves `scripts/install.sh` at
   `https://nomi-lang.org/install.sh`: `make deploy-tour`, the site's build
   command, copies it to the site root. The deploy that publishes the
   release commit publishes it. Check that the served file is the tagged
   one:

   ```console
   $ curl -fsSL https://nomi-lang.org/install.sh | diff - scripts/install.sh
   ```
