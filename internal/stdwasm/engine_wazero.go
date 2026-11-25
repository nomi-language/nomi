//go:build !js

package stdwasm

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// The wazero engine: a runtime of its own a module, whose compilation
// cache is the module's directory under the artifact cache (cache.go), the
// module compiled in it, and its function imports served by one host
// module per import module name, instantiated once a module, whose
// functions find the instance whose call reached them in the call's
// context (instanceKey).

type wazeroModule struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	info     *moduleInfo
	// link is the host modules' instantiation, once, and its error.
	link    sync.Once
	linkErr error
}

func compileEngine(data []byte, sum [sha256.Size]byte, info *moduleInfo) (engineModule, string) {
	ctx := context.Background()
	config := wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV2)
	if cache := compilationCache(sum); cache != nil {
		config = config.WithCompilationCache(cache)
	}
	r := wazero.NewRuntimeWithConfig(ctx, config)
	compiled, err := r.CompileModule(ctx, data)
	if err != nil {
		_ = r.Close(ctx)
		return nil, err.Error()
	}
	return &wazeroModule{runtime: r, compiled: compiled, info: info}, ""
}

// instanceConfig is every instance's: anonymous, running no `_start`, and
// with a source of randomness given, which no instance can read (it would
// take a WASI import, which nothing serves), so wazero does not seed one
// of its own for each.
var instanceConfig = wazero.NewModuleConfig().WithName("").WithStartFunctions().WithRandSource(noRandomness{})

type noRandomness struct{}

func (noRandomness) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func (m *wazeroModule) close() { _ = m.runtime.Close(context.Background()) }

// instanceKey carries, in a call's context, the instance whose call it is.
type instanceKey struct{}

// errAbort stops the wasm a failed import was called from.
var errAbort = errors.New("std::wasm: an import failed")

func apiTypes(ts []valType) []api.ValueType {
	out := make([]api.ValueType, len(ts))
	for i, t := range ts {
		out[i] = api.ValueType(t)
	}
	return out
}

// linkImports instantiates, once, the host modules that serve m's
// function imports: each function answers the instance whose call reached
// it, by its first index.
func (m *wazeroModule) linkImports() error {
	m.link.Do(func() {
		byModule := map[string]wazero.HostModuleBuilder{}
		var order []string
		seen := map[string]bool{}
		for i, imp := range m.info.imports {
			key := imp.module + "\x00" + imp.name
			if seen[key] {
				continue
			}
			seen[key] = true
			b, ok := byModule[imp.module]
			if !ok {
				b = m.runtime.NewHostModuleBuilder(imp.module)
				byModule[imp.module] = b
				order = append(order, imp.module)
			}
			b.NewFunctionBuilder().
				WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, caller api.Module, stack []uint64) {
					inst := ctx.Value(instanceKey{}).(*wazeroInstance)
					if !inst.host(i, caller.Memory(), stack) {
						panic(errAbort)
					}
				}), apiTypes(imp.typ.params), apiTypes(imp.typ.results)).
				Export(imp.name)
		}
		for _, mod := range order {
			if _, err := byModule[mod].Instantiate(context.Background()); err != nil {
				m.linkErr = err
				return
			}
		}
	})
	return m.linkErr
}

// wazeroInstance is an anonymous instance in its module's runtime.
type wazeroInstance struct {
	mod  api.Module
	host hostFunc
	ctx  context.Context
	// limited is the instance's memory, which sets refused when the module
	// asks it to grow past the limit.
	limited *limitedMemory
}

// limitedMemory is an instance's linear memory, allocated through
// experimental.WithMemoryAllocator: it grows to the instance's limit and no
// further, so past it `memory.grow` answers -1 inside the module. Its
// buffer comes from spare when one there is large enough, and goes back
// to it when the instance closes.
type limitedMemory struct {
	buf []byte
	max uint64
	// zero is where the bytes of buf's capacity that are all zero begin:
	// the memory's size, or past it in a buffer an earlier instance used.
	zero    uint64
	refused bool
}

func (l *limitedMemory) Reallocate(size uint64) []byte {
	if size > l.max {
		l.refused = true
		return nil
	}
	if l.buf == nil {
		l.buf, l.zero = spare.take(size)
	}
	if size <= uint64(cap(l.buf)) {
		// The bytes the memory gains are zero, as a new page's are.
		if from := uint64(len(l.buf)); from < l.zero {
			clear(l.buf[from:min(size, l.zero)])
		}
		l.buf = l.buf[:size]
		l.zero = max(l.zero, size)
		return l.buf
	}
	grown := make([]byte, size, min(max(size, 2*uint64(cap(l.buf))), l.max))
	copy(grown, l.buf)
	l.buf, l.zero = grown, size
	return l.buf
}

// Free hands the buffer to spare. wazero calls it when the instance
// closes, after its last call.
func (l *limitedMemory) Free() {
	if l.buf != nil {
		spare.put(l.buf[:l.zero])
	}
	l.buf = nil
}

// spare holds the buffers of closed instances' memories, so a later
// instance's memory, and `wasm.run`'s above all, takes one instead of
// allocating megabytes the collector must then reclaim. A buffer's bytes
// past its length are zero, and an instance clears each byte of it before
// its memory holds that byte, so nothing one instance wrote reaches
// another. It holds at most spareBytes of capacity in all.
var spare buffers

// spareBytes is the most capacity spare holds: 64 MiB.
const spareBytes = 64 << 20

type buffers struct {
	mu   sync.Mutex
	bufs [][]byte
	held uint64
	// reused counts the buffers taken, for the tests.
	reused uint64
}

// take is the smallest buffer held of at least size bytes of capacity,
// empty, and where its zero bytes begin; or a new one, all zero.
func (b *buffers) take(size uint64) ([]byte, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	best := -1
	for i, buf := range b.bufs {
		if uint64(cap(buf)) >= size && (best < 0 || cap(buf) < cap(b.bufs[best])) {
			best = i
		}
	}
	if best < 0 {
		return make([]byte, 0, size), 0
	}
	buf := b.bufs[best]
	b.bufs = append(b.bufs[:best], b.bufs[best+1:]...)
	b.held -= uint64(cap(buf))
	b.reused++
	return buf[:0], uint64(len(buf))
}

// put holds buf, whose bytes past its length are zero, unless that would
// take spare past spareBytes.
func (b *buffers) put(buf []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held+uint64(cap(buf)) > spareBytes {
		return
	}
	b.bufs = append(b.bufs, buf)
	b.held += uint64(cap(buf))
}

func (m *wazeroModule) instantiate(limit uint64, host hostFunc) (engineInstance, error) {
	if err := m.linkImports(); err != nil {
		return nil, err
	}
	inst := &wazeroInstance{host: host}
	inst.ctx = context.WithValue(context.Background(), instanceKey{}, inst)
	ctx := experimental.WithMemoryAllocator(inst.ctx, experimental.MemoryAllocatorFunc(func(_, max uint64) experimental.LinearMemory {
		inst.limited = &limitedMemory{max: min(max, limit)}
		return inst.limited
	}))
	mod, err := m.runtime.InstantiateModule(ctx, m.compiled, instanceConfig)
	if err != nil {
		return nil, err
	}
	inst.mod = mod
	return inst, nil
}

// function looks name up in wazero once an instance, since each lookup
// makes a call engine of its own.
func (inst *wazeroInstance) function(name string, _ funcType) engineFunc {
	return inst.mod.ExportedFunction(name)
}

func (inst *wazeroInstance) call(fn engineFunc, stack []uint64) error {
	if inst.limited != nil {
		inst.limited.refused = false
	}
	return fn.(api.Function).CallWithStack(inst.ctx, stack)
}

func (inst *wazeroInstance) memory() engineMemory {
	if mem := inst.mod.Memory(); mem != nil {
		return mem
	}
	return nil
}

func (inst *wazeroInstance) refused() bool { return inst.limited != nil && inst.limited.refused }

func (inst *wazeroInstance) close() { _ = inst.mod.Close(inst.ctx) }
