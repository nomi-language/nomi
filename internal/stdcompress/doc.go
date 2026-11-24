// Package stdcompress is std/compress/host: gzip (RFC 1952), zlib (RFC
// 1950) and raw deflate (RFC 1951) over Go's compress/gzip, compress/zlib
// and compress/flate. It is the one compressor in the implementation:
// std::http's `compress` middleware and `testing.decompressed` call
// std::compress.
//
// A format crosses as a number (0 gzip, 1 zlib, 2 raw deflate) and a level
// as Go's (-1 the default, 0 stored, 1 to 9); std/compress.nomi decides
// both, and a number outside them is read as the default, so no operand
// makes a crossing fail. A gzip header's name and comment are written in
// ISO 8859-1, as RFC 1952 says, with `?` for a character outside it or a
// NUL, a modification time outside what four bytes of Unix seconds hold is
// written as none, and an OS number past 255 as 255 (unknown).
//
// # Whole values
//
// `encode` compresses Bytes whole. `decode` decompresses them under a limit:
// it reads the decoder through a limit one byte past it, so a stream that
// expands past the limit stops there, having made at most the limit, one
// byte and what the decoder holds (its 32 KiB window), and answers TooLarge.
// `decode_at` reads one stream from an offset, a gzip stream's first member
// only, with the offset just past its end: std::compress's `decode_prefix`
// and `gzip_members` are it. Its decoder reads a strings.Reader, which is
// an io.ByteReader, so Go's decoders read no byte past the stream's end.
// `decode` reads every gzip member as one stream, as Go does. A failure
// crosses as a kind, the format the data's header shows when that is
// another, and a reason.
//
// # Streams
//
// `reader` decompresses a Reader as it is read, `writer` compresses into a
// Writer, and `encoding_reader` compresses a Reader as it is read, each
// read of the source flushed through, so a stream that arrives slowly is
// passed on as it arrives (std::http's streamed bodies). Each is one of rt's
// streams whose bytes are made as they are read or written
// (rt.NewPullReader, rt.NewPushWriter): it reads or writes the stream it
// wraps on the frame of the operation, so a task stopped while a read waits
// unwinds there, and the runtime runs nothing of it once the program no
// longer holds it or has ended. A read stopped that way leaves the decoder
// part-way, so the reader's later reads answer `Other` with `EINTR`.
//
// Closing a reader does not close its source, and closing a writer writes
// the format's end into its sink and leaves the sink open, as Go's readers
// and writers do; a writer's flush flushes the compressor (a sync point)
// and then the sink. An error of the wrapped stream is the operation's
// error as it is; a stream that is not valid is `io.Error.Corrupt` with a
// reason, or `Truncated` when it ends early.
//
// Every whole-value row is Pure. `decode` and `decode_at` are also
// Unbounded, since their cost grows with the limit they are given, a value;
// `encode`'s grows with its data's size only. The stream rows are Held: they
// act through the Reader or Writer they are given (internal/stdlibbindings
// package doc, "Effects and World"). None is in a family, so a program that
// compresses runs on the lean runner.
package stdcompress
