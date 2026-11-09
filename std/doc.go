// Package std is Nomi's standard library: the `.nomi` source of every std
// module, embedded into the binaries (std.go), and the Go side that analyzes
// it once per process ([Shared], [Load], [All]) and makes the artifact a
// release embeds ([StdArtifact]). How the stdlib is built from its modules'
// interfaces and a module at a time is in shared.go's file comment.
//
// This comment says what std holds and what lib/ holds, why std binds Go
// with `host` rather than `go`, and how the std modules written only in
// Nomi are built. The steps for a new host function are in
// internal/stdlibbindings' package doc.
//
// # Std and lib
//
// std holds what Go's standard library can back with no third-party code
// (std/fs, std/sql's vocabulary, std/http over net/http). lib/ holds
// first-party modules that need third-party Go code or are opinionated
// layers over std: the database drivers (lib/sql/sqlite over
// modernc.org/sqlite, lib/sql/postgres over pgx), lib/sql/migrate (the
// migration runner sqlkit's `migrate` calls), lib/sql/sqlkit, lib/markdown
// over goldmark, lib/image over golang.org/x/image, lib/password_hashers
// over golang.org/x/crypto, and lib/jobs/postgres and lib/storage/s3, which
// are Nomi alone over std. A lib module with Go code documents it in its
// `goside` package.
//
// Each lib module is its own module with its own go.mod and nomi.toml,
// nested in this repository, so the compiler's module excludes it and its
// requirements never enter the compiler's go.mod. It is versioned with
// nomi's commits, with no tags. A module that requires a sibling `replace`s
// it with the sibling's directory, and requires a published commit only as
// a floor. `nomi add` and `nomi update` keep every
// github.com/nomi-language/nomi/lib/... module a project requires on one
// commit, the one added or the newest, so a sibling require never needs
// moving when a batch is pushed. `scripts/pin-lib.sh <commit>` moves the
// floors when a module comes to need a newer sibling, and
// `scripts/pin-lib.sh --verify <commit>` adds sqlkit at a pushed commit
// from the proxy into a fresh project and runs sqlite.
//
// TestLibModules in cmd/nomi runs their Nomi tests, the shop example's
// generated-code check and the drivers' Go tests, and checks that
// librunners/ is what `go run ./internal/ffirun/libgen` generates and vets
// it (internal/ffirun package doc, "Prebuilt lib runners"). It first runs
// `go mod download` in each driver, which needs the network when the Go
// module cache lacks modernc.org/sqlite or pgx. Its postgres runs skip
// without `initdb` and `postgres` on PATH and on Windows. CI's lib job vets
// and tests their Go side. By hand:
//
//	go test -C lib/sql/sqlite ./...
//	go test -C lib/sql/postgres ./...
//
// and `go vet` the same way in each lib module. The tour links a lib
// module's browser build through a workspace (internal/tourgo package doc).
//
// # Why std uses host rather than go
//
// `host` means the implementation is outside Nomi and the standard library
// owns it. Where the Go code sits is a layout question the binding table
// (internal/stdlibbindings) answers, not something the declaration spells.
// That is OCaml's arrangement under one keyword rather than two:
// `external length : string -> int = "%string_length"` marks a compiler
// primitive and `external open : ... = "unix_open"` marks a C stub, both
// `external`, with the symbol saying which.
//
// The implementation sits in one of two places:
//
//   - rt, the runtime library the VM runs over. The language primitives:
//     `String.trim`, `Map.get`, `Int.add`, `Bytes.length`, the `Iter`
//     adapters.
//   - A package in the compiler's module (internal/stdcalendar,
//     internal/stdio, internal/stdregex, ...). These wrap a library or an
//     OS facility, and keeping them out of rt puts their dependency and
//     their binary size on the programs that ask for them. std/calendar is
//     why a hello-world artifact contains no `time/tzdata`. The stdlib is
//     one Nomi module, so such a package is not separately distributable
//     and carries neither nomi.toml nor go.mod.
//
// Why std does not use the `go` spelling:
//
//   - It would need a Go toolchain. A `go`-bound module makes
//     internal/ffirun stage and build a wrapper, so a program importing it
//     needs a Go toolchain; on a machine without Go, the first `nomi run`
//     downloads one (the managed toolchain, about 70 MB) and every new set
//     of bindings is a Go build. internal/ffirun's
//     hostkeyword_toolchain_test.go asserts that no stdlib import takes
//     that path.
//   - It pays the FFI projection on every call. An rt-backed `host fn` is
//     called with the VM's own values; a `go` binding goes through the FFI
//     projection, which converts each argument into a Go value and the
//     result back. `Bytes` crossing as `[]byte` makes an O(1) length read
//     cost an O(n) copy.
//   - rt's own types do not project. `rt.Bytes` is a Go string, so the
//     boundary would read it as a Nomi `String`, and `rt.Maybe[rt.Byte]` is
//     a struct, so a `Maybe<Byte>` return would arrive as a record of its Go
//     fields.
//
// A user's `go` binding pays the same projection, which is correct: it
// crosses a real FFI boundary into code the compiler does not own.
//
// # Modules written only in Nomi
//
// The modules below have no Go package of their own. Each is lowered with
// each program (`programStdModules`, internal/irbuild/programstd.go), so its
// structs can stand in public signatures. Where one crosses into Go, the
// crossing is named with the package that implements it.
//
// # Logging and actors
//
// std/log, std/log_testing and std/actors are Nomi alone, no `host fn`. A
// `Logger` is an opaque struct over a private `Sink`: text, JSON, discard,
// or the recorder's, which writes each record as one JSON array (time in
// nanoseconds, level, message, then `[key, kind, value]` per field) that
// `log_testing.run` decodes into `Record`s; an array keeps field order,
// which a JSON object does not. Each record is one `Writer.write_text`; a
// failed write is dropped. std/log names a level `Debug` and a std file has
// no prelude, so it imports `debug.Debug` for the synthesized `impl Debug`
// headers, which the unused-import check counts as a use.
//
// An actor is a task under the caller's supervisor looping over
// `Receiver.receive` on its inbox (64 messages unless `Actor.start` says
// otherwise), folding each message into its state, ending when the inbox
// closes. `Actor.call` sends a message around a one-slot reply channel and
// waits with `Receiver.receive_within` (five seconds by default). Both
// defaults sit before the trailing function, so an omitted one is filled as
// in any cross-file call: the callback goes to the last slot and the
// instance's accessor fills the default (`slottedOperands`,
// `methodInstAccessors` in internal/irbuild).
//
// # Background jobs
//
// std/jobs and std/jobs_testing are Nomi alone. A `Job<A>` erases to a
// `Handler` through `impl Handler for Job<A> where A: FromJson`, so a worker
// keys a `List<Handler>` of mixed argument types by kind in a `Map`. No
// `Handler` or `Queue` function returns `self` or takes two `self` operands,
// so both dispatch on interface values. `jobs.enqueue<A>` is a free function
// writing through `Queue.insert` on the caller's `Conn`.
//
// `run_one` runs the handler in a task plus a second task that waits on
// `Task.outcome` and sends it on a channel, so the worker's `select` over
// that channel and `Arm.after(timeout)` sees a trap at once and a timeout
// before the handler ends; a timeout calls `Task.cancel`, which reaches a Go
// call through the FFI context rule. A library cannot set `App.context`, so
// the timeout is the worker's.
//
// `Worker.new` creates two supervisors, so it can run while boot builds the
// app value: `loops` (`.Transient`, shutdown timeout two seconds longer than
// the jobs') holds the claim loop, the queue's watcher and the scheduler;
// `runs` holds the jobs, with `max_running`. Handlers arrive in
// `Worker.start`, in `main`, because boot runs what it references and a
// handler reads app fields. The claim loop takes a slot (a buffered channel
// of `max_running` units), claims one job, spawns it under `runs`, and
// charges the rate's GCRA token bucket; an empty claim returns the slot,
// runs `rescue` and `prune`, and waits on the wake channel or the poll.
// Drain closes the wake channel; the loop then flushes `runs` for the grace
// period, by which time `runs` has cancelled the rest, and sends `release`
// for its worker id. A task beside the handler renews the lease every third
// of it.
//
// A unique key is resolved in Nomi: `Unique.key_for` writes kind, key and,
// with a period, the period's start as one JSON array, so no two part lists
// write one key; `NewJob` carries the string and the states it is held in.
// A backend compares strings only. Priority orders claims before due time.
//
// Schedules. Given any, `Worker.start` runs a loop in `loops` that calls
// `enqueue_due` per schedule, then waits on a stop channel (closed on
// drain) or the earliest next tick. `enqueue_due` reads `Queue.last_tick`,
// finds the latest tick at or before now, and passes its job to
// `Queue.record_tick`, which records the tick and inserts the job in one
// transaction. The job's unique key is the schedule's name and the tick,
// held across every state, so a racing second process's duplicate is turned
// away by the key; the record only spares the insert. With no record a
// scheduler records now and enqueues nothing, which tells a new schedule
// from one that was down. Interval ticks are whole multiples since the
// epoch, so processes agree without talking. A cron tick is found on Int
// wall times (month, day, hour, minute, each step jumping to the next
// possible start, with Vixie cron's either-day rule), then resolved through
// std/calendar: a fold takes `Disambiguation.Earlier` and the search
// continues past a time not after the start, so the second occurrence never
// ticks; a gap resolves to the transition, found by bisecting seconds
// between the `Earlier` and `Later` readings. The latest tick at or before
// now comes from a window before now that doubles until it holds a tick,
// then a walk forward. `Cron` is a `CheckedLiteral` refusing any slot, so
// `build` cannot fail; `Cron.parse` is the same at check and run time.
//
// Postgres. lib/jobs/postgres is Nomi over lib/sql/postgres. Renew, settle
// and release match on id, attempt and worker, so a settle after a rescue
// changes nothing; the table name option rewrites `nomi_jobs` in each
// statement. The watcher relays `Listener.forward` into a `select` with the
// drain-closed stop channel, so it ends when shutdown starts. A unique key
// is a partial unique index over rows that hold one
// (`unique_states = 'kept' OR state IN ('available', 'running')`); an insert
// is `ON CONFLICT ... DO NOTHING`, then, if nothing was stored, a select of
// the holder (the insert waits for an open transaction holding the key, so
// the select sees its outcome). Ticks are rows of `<table>_schedules`,
// upserted with `GREATEST`. `install` also runs schema_upgrade.sql's
// `ADD COLUMN IF NOT EXISTS` statements. `jobs_testing.conformance` is the
// backend suite: `Inline` passes it without the rollback rule, and
// lib/jobs/postgres passes all of it on `postgres.scratch()`.
//
// # Mail
//
// std/mail and std/mail_testing are Nomi. Two encodings are Go's, through
// std/mail_host (rt/mail.go, pure): quoted-printable bodies
// (mime/quotedprintable) and the `Date` header (RFC1123Z in UTC). The SMTP
// client is Nomi over std/net's `Connection`: it upgrades with `tls.client`
// for STARTTLS or dials `tls.dial` for implicit TLS, and reads replies with
// `Reader.read_line`. So mail inherits std/net's `connect` class (no row of
// its own for `nomi build`'s target check), its cancellation rule, and the
// network runner. Go's net/smtp is frozen, takes no context and has no
// implicit TLS. `SmtpConfig.timeout` is set as the connection's deadline
// before each command and reply. One send is one connection.
//
// `mail.render` is the one encoder every backend (`Smtp`, `Memory`, others)
// calls, and it checks the message before anything connects. Output is
// 7-bit: RFC 2047 base64 encoded words, cut between graphemes (Go's
// `mime.BEncoding` cuts between runes, so this stays in Nomi), for a
// subject, display name or header value that is not printable ASCII or
// holds `=?`; quoted-printable bodies; base64 attachments; CRLF; header
// lines folded before spaces at 78 characters. Boundaries are fixed strings
// starting `=_`, which neither encoding can produce, so output is
// deterministic for a given Message-ID and date.
//
// `mail_testing.with_smtp_server` serves one connection at a time on a task
// of its `concurrent` block, with `http_host.test_certificate` for
// STARTTLS; `conformance` runs against `Memory` and `mail.smtp` on it in
// std's `//!` cases. vmhost/mail_smtp_test.go runs a program against a Go
// fake server (STARTTLS with AUTH PLAIN and LOGIN, implicit TLS, no TLS)
// and parses what arrives with Go's net/mail, mime and mime/multipart,
// independent parsers.
//
// # Sessions
//
// std/sessions is Nomi. Its two crossings in std/http_host, `seal` and
// `open_sealed`, are AES-256-GCM (rt/seal.go) with a random 12-byte nonce
// before the ciphertext. A cookie's value is that, in unpadded base64url,
// over a JSON object: `c` and `s` (Unix seconds the session began and was
// last written), `f` (flash messages for the next request) and `v` (the
// app's value through `ToJson`, absent for none). The additional data is
// `nomi session 1 <cookie name>`, so a value sealed for one cookie does not
// open as another, and a format change is a label change.
// `SessionKey.derive` is the secret's HMAC-SHA256 under a fixed label.
// `open` tries `key`, then each of `previous_keys`; anything that fails to
// open, decode or parse, or is past `max_age` or `idle_timeout`, is no
// session. `with_session` writes nothing when the response already sets the
// cookie (`Response.cookies`); otherwise it deletes a cookie that did not
// open and rewrites one whose flash was read or whose last-seen time is a
// minute old under an idle timeout. `write` replaces any `Set-Cookie` of
// the same name, so the last write wins. There is no server-side store; one
// would keep an id in the same sealed cookie behind the same `Session` and
// `write`.
//
// # Passwords and JWTs
//
// std/passwords, std/signatures and std/jwt are Nomi. passwords' one
// crossing is `hash.pbkdf2_sha256` (crypto/pbkdf2 in rt/digest.go), salted
// from `random.secure_bytes`. A stored hash is a PHC string (`Phc`);
// `verify` picks the first `PasswordHasher` whose `recognizes` accepts the
// string's algorithm id, so a module adds an algorithm by implementing the
// interface. lib/password_hashers adds Argon2id (written with std's `Phc`)
// and bcrypt (its own `$2a$` encoding passed through) over
// golang.org/x/crypto. Every hasher compares with
// `hash.equal_in_constant_time`.
//
// std/signatures crosses into internal/stdhttp (its package doc,
// "Signatures"). jwt uses `hash.hmac_sha256` for HS256 and
// `signatures.sign_with` and `is_valid_with` for the rest; a key holds the one
// algorithm it was made for, which a token's `alg` must name.
//
// # Object storage
//
// std/storage and std/storage_testing are Nomi alone. No `Store` function
// returns `self` or takes two `self` operands, so it dispatches on interface
// values; `storage.local` and `s3.store` each return a `Store` over a
// private struct, so boot code can pick a backend with an `if`.
//
// Keys. Every backend calls `check_key` first, and `check_content_type` in
// `put`. The key grammar (no empty segment, no segment starting with `.`,
// no control character, no `\`) is what keeps the local store inside its
// root, with no path cleaning. Every other character is allowed, as in S3,
// so the local store writes `%`, characters Windows refuses, a trailing `.`
// or space, and a device name's first letter as `%XX` (`file_names`);
// escaping `%` keeps the mapping one-to-one. `storage.join_key` escapes each
// segment for a public URL, since `Url.join_path` takes segments as already
// escaped and drops a join whose `%` is not an escape. There is no rule
// that keys `a` and `a/b` coexist: S3 allows it and a directory tree
// cannot.
//
// Local store. It goes through std/fs, so `fs_testing.run` sandboxes it.
// `put` writes the content type to `<root>/.nomi-storage/types/<key>`, then
// the bytes to `<root>/<key>`, each with `WriteMode.ReplaceAtomically`,
// whose temporary file starts with `.` so no key names it. A key whose path
// is a directory or runs through a file is `NotFound`; a file without a
// type file is typed by `http.detect_content_type`. `storage.handler`
// responds through `Response.content` (Go's `ServeContent`, for ranges and
// conditional requests), then sets the stored type on a 200 or single-range
// 206.
//
// Listing. `Store.list` pages by `storage.page`: entries under the prefix
// in key order, each key holding the delimiter after the prefix folded into
// its common prefix and deduplicated, then those after the continuation,
// `limit` of them; the continuation is the last key or prefix shown. Keys
// under one prefix are contiguous, so a prefix continuation skips all of
// them. `Memory` and the local store use it; the local store walks its
// root, skips every path with a segment starting with `.` (types directory,
// temporary files) and decodes `%XX`. S3's `ListObjectsV2` pages on the
// server with an opaque token.
//
// `Memory` keeps a `Map<String, Object>` in a `Channel<State>` of capacity
// one, as `jobs_testing.Inline` does. `storage_testing.conformance` runs
// each rule on a fresh store from `open`; `Memory` passes it, and the
// corpus runs the local store through it in a sandbox and in a temporary
// directory.
//
// S3. lib/storage/s3 is Nomi over std/http, no Go. Its signer follows SigV4
// for S3: one URI encoding of the path (no double encoding, no `.`
// normalization), the query re-encoded and sorted, every header signed with
// `host`, and the body's SHA-256 or `UNSIGNED-PAYLOAD`. The path is escaped
// for every byte outside `A-Z a-z 0-9 - _ . ~` by its own `uri_encode`
// (std/url's escapers keep `$` and write a space as `+`), also used for a
// listing's query, and the request's `Url` carries it in `raw_path` when it
// differs, so Go sends what was signed. Error documents and `ListObjectsV2`
// pages are read with std/xml. Each attempt is signed afresh; one that ends
// `Unavailable` (transport failure, 429 or 5xx) retries after 100 ms, then
// 400 ms, plus up to half again from the clock's nanoseconds. A library
// cannot read the context, so a retry does not check the deadline; a passed
// deadline unwinds the task. Tests sign AWS's three published examples and
// use std/http_testing, including a fake server over a `Memory` that runs
// the conformance suite and refuses a request whose escaped path differs
// from `uri_encode`'s. lib/storage/minio runs the suite against a real
// MinIO; TestLibModules skips it without `minio` on PATH.
package std
