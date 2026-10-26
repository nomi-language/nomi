package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// `Reader.from_chunks` and `Reader.from_text_chunks`, which the builder
// lowers as crossings over the chunks viewed as a sequence
// (internal/irbuild/irreader.go).
const (
	readerFromChunksKey     = "io.Reader.from_chunks"
	readerFromTextChunksKey = "io.Reader.from_text_chunks"
)

// readerFromChunksHost answers both: a Reader that pulls the sequence one
// chunk per read that needs one (rt.ReaderFromEach). The sequence runs on
// the frame of the read that starts it, and a fault in it, a trap included,
// is the fault of that read.
func readerFromChunksHost(name string) hostFn {
	return func(_ *Machine, _ ir.Pos, args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("vm: %s: expected one operand, got %d", name, len(args))
		}
		chunks, ok := args[0].(rt.Seq[any])
		if !ok {
			return nil, fmt.Errorf("vm: %s: the chunks are %T, not a sequence", name, args[0])
		}
		return rt.ReaderFromEach(func(fr *rt.Frame, yield func(rt.Bytes) bool) {
			chunks.Run(fr, func(_ *rt.Frame, item any) bool {
				switch chunk := item.(type) {
				case rt.Bytes:
					return yield(chunk)
				case string:
					return yield(rt.Bytes(chunk))
				}
				panic(iterationFailure{fmt.Errorf("vm: %s: a chunk is %T", name, item)})
			})
		}), nil
	}
}
