package rt

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Startup is the immutable process-input snapshot supplied to boot.
type Startup struct {
	Env  Map[string, string]
	Args *List[string]
}

// NewStartup copies explicit host inputs into immutable Nomi collections.
func NewStartup(environ, args []string) Startup {
	var s Startup
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			s.Env = MapPut(s.Env, HashString, func(a, b string) bool { return a == b }, key, value)
		}
	}
	s.Args = ListFromGo(args, func(s string) string { return s })
	return s
}

func StartupSnapshot() Startup { return NewStartup(os.Environ(), os.Args[1:]) }

// ActiveContext reads the current scoped execution context.
func ActiveContext(fr *Frame) Context {
	if fr != nil && fr.scopedContext != nil {
		return *fr.scopedContext
	}
	return ContextRoot()
}

func EnterContext(parent *Frame, context Context) *Frame {
	child := *parent
	child.scopedContext = &context
	return &child
}

// InstallContext publishes boot's returned context on its execution frame.
func InstallContext(fr *Frame, context Context) {
	fr.scopedContext = &context
	fr.booted = fr.booted.withContext(&context)
	child, release := EnterDeadline(fr, context)
	*fr = *child
	DeferBoot(fr, release)
}

type bootCleanupState struct {
	mu      sync.Mutex
	once    sync.Once
	calls   []func()
	failure error
}

func DeferBoot(fr *Frame, cleanup func()) {
	if fr.bootCleanup == nil {
		fr.bootCleanup = new(bootCleanupState)
	}
	state := fr.bootCleanup
	state.mu.Lock()
	state.calls = append(state.calls, cleanup)
	state.mu.Unlock()
}

// RunBootCleanup releases resources once, attempting all calls in reverse order.
func RunBootCleanup(fr *Frame) {
	if fr == nil || fr.bootCleanup == nil {
		return
	}
	state := fr.bootCleanup
	state.once.Do(func() {
		state.mu.Lock()
		calls := state.calls
		state.calls = nil
		state.mu.Unlock()
		var failures []string
		for i := len(calls) - 1; i >= 0; i-- {
			func() {
				defer func() {
					if r := recover(); r != nil {
						failures = append(failures, fmt.Sprint(r))
					}
				}()
				calls[i]()
			}()
		}
		if len(failures) > 0 {
			state.failure = &Error{Msg: strings.Join(failures, "; cleanup error: ")}
		}
	})
	if state.failure != nil {
		panic(state.failure)
	}
}

// DeferBootAt preserves the source location of a resource cleanup failure.
func DeferBootAt(fr *Frame, line int, cleanup func()) {
	DeferBoot(fr, func() {
		defer func() {
			if r := recover(); r != nil {
				panic(DeferredCleanupError(line, r))
			}
		}()
		cleanup()
	})
}

func DeferredCleanupError(line int, failure any) error {
	return &Error{Msg: fmt.Sprintf("line %d: deferred cleanup failed: %v", line, failure)}
}
