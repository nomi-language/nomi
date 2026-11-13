// Package stdhttp is std/http/host and std/signatures/host: the HTTP
// client and server over Go's net/http, TLS and TCP for std/tls and
// std/net, the WebSocket crossings, and signing keys. HTTP, TLS and TCP
// share this one adapter package. internal/hostgen/stdtable's
// alsoImplements names it as both modules', and the adapter generator
// moves it to the runner with the network (`nomi-vm`, not `nomi-vm-nonet`).
//
// std/url, std/http, std/tls and std/net are lowered with each program
// (`programStdModules`), so their structs can stand in public signatures.
// std/url crosses through std/url/host (rt/url.go, pure), the others through
// std/http/host here.
//
// inventory.txt lists every exported identifier of Go's HTTP packages, what
// Nomi does with it and the phase that lands it. TestGoInventoryIsComplete
// holds it to the Go the tests run under, and every covered row of a landed
// phase ([LandedPhase]) to a std declaration.
//
// # Client
//
// A `Url` crosses as its 14 parts; a request as method, URL text, headers as
// alternating names and values, and the body as bytes or a `Reader`.
// `std::http::host.send` returns an `Exchange` handle (rt.HTTPExchange) whose
// accessors read the response, or an `http.Error` variant's name and
// fields. A `Client` is a handle (rt.HTTPClient) over Go's http.Client,
// which owns the pool; `http.default_client()` is one Go client per
// process. `send` carries the `network` class, which wasip1 lacks. On
// js/wasm the transport sets no dial function so Go keeps its `fetch` path
// (transport_js.go).
//
// `std::http::testing.run` is three VM crossings (internal/vm/network.go):
// `run_on` installs the `answer` function on the frame (rt.EnterNetwork),
// `answer_on` calls it and records the request, and `recorded_on` returns
// the requests. The builder lowers them like the file `run_on` (`answerOn`,
// `recordedOn` in internal/irbuild/ircapture.go).
//
// With `Redirects.Decide` the Go client follows nothing and
// `send_following` follows on the calling task, since Go would run the
// callback on its own goroutine.
//
// # Server
//
// Go's http.Server (server.go) calls its handler on a goroutine per
// request; the handler hands the request to the program over a channel
// (`std::http::host.next`) and waits for `std::http::host.finish`, so no Nomi code runs
// on Go's goroutines. `http.serve` loops on `next` and spawns each request
// on its supervisor. The request task runs its handler through
// `std::http::host.request_on` (`networkRequestHost` in internal/vm, lowered by
// `requestOn`): the handler's context is the active one with the server's
// `request_timeout` as deadline, on a frame whose app `Context` reads it and
// which stops when the client goes away (rt.EnterRequest). Every write to
// the `ResponseWriter` holds the request's lock, and after Go's handler
// returns a write fails. A handler that wrote nothing gets a 500 from the
// deferred `finish`; a request task a supervisor restarts finds the
// response sent and returns. `Body.Produce` writes through an rt `Writer`
// that flushes to the client on each write.
//
// `request_of` builds the `Request` from the incoming accessors. Go's
// `r.URL` holds only the target, so `served_url` adds the scheme (`https`
// when `in_tls` reports a handshake) and the `Host` header's host (the
// local address when it is empty), making a served `url` absolute like
// `std::http::testing.request`'s, which sets `host` to its URL's host. An
// absolute-form target and `OPTIONS *` are left as Go read them.
//
// `std::http::host.listen` carries the `listen` class (not on js/wasm or
// wasip1). A listener registers a drain hook (rt.OnDrain), so draining runs
// `Server.Shutdown` before any grace period: accepting stops, taken requests
// finish, `serve` returns and the drain ends. `http.serve_here` serves
// requests as tasks of the enclosing `concurrent` block;
// `std::http::testing.with_server` uses it with a certificate from
// `std::http::host.test_certificate`.
//
// # Routing
//
// `Router` builds Go's ServeMux (`std::http::host.new_mux`) with handlers that
// only record the matched pattern and wildcard values; `std::http::host.route`
// runs the mux on a recorder and returns the match or what the mux wrote
// (redirect, 405, 404). Pattern syntax, precedence, conflicts and path
// cleaning are Go's. `Route<P>` is a checked literal whose `check` adds two
// rules before Go's (`std::http::host.check_pattern`): the method is a named
// `Method` variant, and a brace stands only in a whole-segment wildcard;
// `check_type` holds the wildcards to `P`'s fields. `Route.parse` and
// `Route.with_method` (which skips the method rule) apply them at run time,
// fields through `ParseFields.field_names`. A route literal with no
// expected type is typed from its wildcards
// (internal/analysis/checked_literal_args.go), and `P`'s fields are held to
// `Parse` and `Display` there. `Route.to` and `to_with` make an `Entry` (a
// pattern and `(Request) -> Maybe<Response>`, `None` a 404); `to_with`
// reads path values through `parse.read_fields`. `ParseFields` at an
// anonymous struct has no impl: the checker admits it when each field
// implements `Parse` and `Display`
// (internal/analysis/structural_parse_fields.go), and irbuild lowers its
// three functions at the bound record kind (internal/irbuild/irparsefields.go),
// and at `Unit` too, since no receiver selects std's impl.
//
// # Go helpers on recorders and strings
//
// `Response.redirect` and `Response.content` run Go's `Redirect` and
// `ServeContent` on a recorder; `http.serve_file` reads through std/fs and
// hands the bytes to `ServeContent`. Cookies cross as field lists; a
// `CookieJar` is Go's cookiejar (rt.HTTPJar). `Headers.write_to`
// (`Header.WriteSubset`), `Protocol.parse` (`ParseHTTPVersion`),
// `Request.basic_auth` and `Request.add_cookie` cross as plain strings. A
// multipart form crosses as a flat `List<Bytes>` (`std::http::host.parse_multipart`,
// `encode_multipart`) over bytes already read; `Body.Multipart`'s boundary
// is a digest of its parts, so the separately computed `Content-Type` and
// bytes agree.
//
// # Body limits
//
// Form readers read through `read_capped` (Nomi, std/http.nomi), which
// copies at most one byte past the limit into `Writer.collect` and leaves
// the rest unread. The limit is the request's `body_limit`
// (`Request.limit_body`, which also applies MaxBytesReader semantics;
// `Server.max_body` sets it on every request in `answer`), else 10 MB for
// URL-encoded and 32 MB for multipart. A read cut off exactly at
// `body_limit` is `Error.BodyTooLarge`. `Request.text` and `json_as` read
// through it with the URL-encoded default.
//
// # Middleware
//
// Each middleware (`timeout`, `cross_origin_protection`, `cors`,
// `log_requests`, `security_headers`, `request_id` and the rest) is a Nomi
// function taking the handler first and returning one. Only `compress`
// crosses to Go (compress.go): a whole body goes through
// `std::http::host.gzip_bytes`; a `Body.Stream` through `gzip_reader`, which
// flushes the compressor after each source read so a slow stream is not
// held; a `Body.Produce` writes into `gzip_writer`, whose
// `rt.WriteSink.Flush` flushes the compressor and the writer under it at
// each `Writer.flush`. `std::http::testing.decompressed` reads back through
// `std::http::host.gunzip`. `method_override` reads a URL-encoded `POST` body
// through `read_capped` and passes on a request whose body is those bytes.
// `cross_origin_protection` builds one `Router` per bypass pattern
// (overlaps are fine there; one router refuses them) and asks
// `CrossOrigin.check`, which runs Go's `CrossOriginProtection.Check`
// (`std::http::host.cross_origin_check`) over the Nomi request's method, `Host`
// and headers, not `Request.incoming`, since an earlier middleware may have
// changed the headers.
//
// # TLS and TCP
//
// std/tls and std/net cross through std/http/host too (tls.go, netconn.go).
// A `ClientConfig` or `ServerConfig` is a Nomi value; each use builds Go's
// *tls.Config (`std::http::host.tls_config`, then `tls_with_certificate` per
// certificate) as a `TlsConfig` handle. A `Certificate` is a handle over
// tls.Certificate, its key never read back. A `Connection` (rt.NetConn)
// wraps a net.Conn (TCP or *tls.Conn) with one rt `Reader` and one
// unbuffered `Writer`; a context.AfterFunc on the dialing task's context
// sets a past deadline when it ends, so a cancelled task's I/O stops. A
// listener (rt.NetListener) accepts on a goroutine into a channel, so
// `Listener.accept` can stop for cancellation or an accept deadline, and
// registers a drain hook that closes it. `net.Error` sorts Go errors with
// errors.Is against system error numbers (not matched on Windows).
// `dial_with`, `tls_dial` and `lookup` carry the `connect` class (not on
// js/wasm, which offers only `fetch`, or wasip1), where each returns the
// no-sockets error (`noSocketsMessage`).
//
// # Hooks
//
// Go calls a program's hooks on its own goroutines, where no Nomi code may
// run. A broker (broker.go) carries each call to the Nomi task waiting in
// the crossing that set it off: a server's by `std::http::host.next`; a client's
// by `send`, which runs `Do` on a goroutine and serves the broker until it
// returns; a TLS configuration's by whichever of those adopts it, or by the
// handshake (`tls_dial`, `tls_client`, `tls_server`, and a TLS listener's
// accept, which handshakes there rather than on first read). A hook that
// returns a result (`certificate_for`, `verify`, `on_proxy_connect`) blocks
// Go's goroutine until the task runs it; one that only reports (connection
// states, the error log, `on_shutdown`, HTTP/2's error counter, the key
// log) is queued, and `next` runs what is left before reporting shutdown.
// A hook that traps traps the waiting task. A TLS state crosses as part
// lists (`stateParts`), the same for a connection, request, response and
// trace event.
//
// # Raw messages and the proxy
//
// `Response.upgrade` takes the connection with
// ResponseController.Hijack, writes the status line and headers itself, and
// hands the handler a `Connection` that reads Go's buffered bytes first.
// `Request.read` and `Response.read` run ReadRequest and ReadResponse under
// a bufio.Reader over a one-byte-per-read reader, so no byte of the next
// message is taken. `http.reverse_proxy` is Nomi: it sends once with the
// client's handle, following no redirect, and returns a `Body.Produce`
// copying the upstream body, flushing per `flush_interval`.
// `std::http::host.Handle` (rt.HTTPHandle) holds Go values with no handle type of
// their own: a ClientHello, a tracer, a read message, a multipart reader
// and its parts.
//
// # WebSocket
//
// std/websocket is Nomi over eleven crossings in std/http/host
// (websocket.go); the protocol is internal/websocket, standard library
// only, since Go has none. Framing, masking, the UTF-8 check, ping replies
// and the limit are per-frame byte work that is slow in the VM, and
// serialized writes need a Go mutex, so the protocol is Go rather than Nomi
// over `Connection`. `websocket.accept` checks the handshake in Nomi and
// returns `Response.upgrade`, so middleware sees an ordinary `101` and the
// connection runs in `respond` on the request's task after middleware
// returns (`compress` passes `1xx` untouched). `ws_open` wraps the hijacked
// net.Conn and clears Go's deadlines on it. `connect` dials with std/net or
// std/tls, and `ws_connect` runs the client handshake in Go, reading the
// response through its own bufio.Reader, which the connection keeps, so no
// frame behind the `101` is lost and the `Connection`'s `Reader` is never
// read.
//
// A `Conn` (a `Handle`) has one reader at a time and one write lock that
// every frame takes, pongs and interval pings included, so frames from
// several tasks interleave whole. A blocking call stops when the calling
// task's context ends (past deadline via context.AfterFunc). `receive`
// returns `Bytes` with a first byte of 1 for text or 2 for binary. A close
// frame is echoed and the connection closed; `close` sends one and reads
// until the peer's or five seconds, or waits for the task already reading.
// A protocol error sends RFC 6455's code (1002, 1007, 1009) and closes.
// Each connection's drain hook closes with 1001.
// `std::http::testing.open_websocket` runs `on_connection` on a task over
// `ws_pipe`, a buffered in-memory pair, since net.Pipe's unbuffered ends
// deadlock when both sides send a close frame. internal/websocket's tests
// feed hand-built frames (Autobahn sections 1 to 7 and 9, as a table);
// TestWebSocket_EndToEnd runs wss through the middleware stack on
// 127.0.0.1.
//
// # Signatures
//
// std/signatures crosses through the internal std/signatures/host
// (signatures.go). A private key crosses as PKCS #8 DER and a public key as
// PKIX DER, held in the opaque `PrivateKey` and `PublicKey` with their kind
// and parsed again by each crossing (for RSA, most of a sign's or verify's
// cost). The encodings are crypto/x509's, which links net, so the functions
// live here, the package the adapter generator moves to the network runner;
// a program that never touches a key keeps a runner without crypto/x509
// (cmd/nomi-vm's deps test). `EcdsaSha256` signatures are r and s as 32
// bytes each, converted from Go's ASN.1; `EcdsaSha256Der` is Go's form.
// std/jwt signs through these (package std's doc, "Passwords and JWTs").
package stdhttp
