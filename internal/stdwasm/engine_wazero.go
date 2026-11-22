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
// further, so past it `memory.grow` answers -1 inside the module.
type limitedMemory struct {
	buf     []byte
	max     uint64
	refused bool
}

func (l *limitedMemory) Reallocate(size uint64) []byte {
	if size > l.max {
		l.refused = true
		return nil
	}
	if size <= uint64(cap(l.buf)) {
		l.buf = l.buf[:size]
		return l.buf
	}
	grown := make([]byte, size, min(max(size, 2*uint64(cap(l.buf))), l.max))
	copy(grown, l.buf)
	l.buf = grown
	return l.buf
}

func (l *limitedMemory) Free() { l.buf = nil }

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
	mod, err := m.runtime.InstantiateModule(ctx, m.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions())
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
