package vm

import (
	"fmt"
	"os"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Boot starts the program: it runs the boot one of this machine's modules
// records and answers a view of the machine whose calls run in the app boot
// published. A program without a boot answers the machine unchanged.
//
// Boot runs on a boot frame with the startup snapshot; its result's fields
// are published on the frame every later call inherits, so an app-field read
// resolves through the same scoped lineage a replacement enters and leaves,
// and a Context field among them becomes the active context. args are the
// program arguments `Startup.args` holds.
//
// A recorded boot this program did not retain is refused rather than skipped:
// an entry started without its app would skip boot's effects and read fields
// nothing published.
func (m *Machine) Boot(args ...string) (*Machine, error) {
	if m.boot == nil {
		return m, nil
	}
	root := rt.NewFrame(m.background())
	return m.bootWith(m.boot, startupValue(args, m.hostEnv), root)
}

// BootTest starts, on a fresh frame, the app a `tests` group's `boot` line
// builds, and answers the view whose calls run in it. group.Boot must be a
// boot one of this program's modules records with ir.Module.AddTestBoot. Each
// call runs the startup function and the boot again and publishes a fresh
// app, because each case gets its own.
func (m *Machine) BootTest(group ir.TestGroup) (*Machine, error) {
	return m.bootTestOn(group, rt.NewFrame(m.background()))
}

// bootTestOn starts the boot a `tests` group's `boot` line calls on root, the
// frame rt's test runner hands a case, so the app is published where that
// case's body runs and rt's per-case cleanup sees what boot registered. The
// group's startup function runs first, on the same boot frame, and its value
// is the Startup the boot receives. A group whose boot takes no parameter
// has no startup function.
func (m *Machine) bootTestOn(group ir.TestGroup, root *rt.Frame) (*Machine, error) {
	if !m.testBoots[group.Boot] {
		return nil, fmt.Errorf("vm: %s is not a test-group boot this program records", group.Boot.Name())
	}
	if group.Startup == nil {
		return m.bootWith(group.Boot, nil, root)
	}
	f, err := m.resolveFunc(group.Startup, "boot startup")
	if err != nil {
		return nil, err
	}
	startup, err := m.callWithFrame(f, nil, rt.EnterBoot(root))
	if err != nil {
		return nil, err
	}
	return m.bootWith(group.Boot, startup, root)
}

func (m *Machine) addTestBoots(mod *ir.Module) {
	for _, sym := range mod.TestBoots() {
		if m.testBoots == nil {
			m.testBoots = map[*ir.Symbol]bool{}
		}
		m.testBoots[sym] = true
	}
}

// bootWith runs boot sym on root and publishes its app. A boot that takes no
// parameter is called with none; one that takes a Startup receives startup,
// which must then be non-nil.
func (m *Machine) bootWith(sym *ir.Symbol, startup any, root *rt.Frame) (*Machine, error) {
	f, err := m.resolveFunc(sym, "boot")
	if err != nil {
		return nil, err
	}
	var args []any
	switch len(f.Params()) {
	case 0:
	case 1:
		if startup == nil {
			return nil, fmt.Errorf("vm: %s takes a Startup and none was given", sym.Name())
		}
		args = []any{startup}
	default:
		return nil, fmt.Errorf("vm: %s takes %d parameters, not a boot's at most one Startup", sym.Name(), len(f.Params()))
	}
	app, err := m.callWithFrame(f, args, rt.EnterBoot(root))
	if err != nil {
		return nil, err
	}
	if e, isEnum := enumRecord(app); isEnum && ((e.Desc.Name == "results.Result" && variantName(e) == "Err") ||
		(e.Desc.Name == "maybe.Maybe" && variantName(e) == "None")) {
		// A `try` in boot's body left it with the failing value, which is
		// reported as the program's failure.
		text, err := rt.DebugText(e, nil)
		if err != nil {
			return nil, err
		}
		return nil, &bootFailed{text: "boot failed: " + text}
	}
	s, isStruct := app.(*rt.Record)
	if !isStruct || s == nil || (s.Desc.Kind != rt.KindStruct && s.Desc.Kind != rt.KindAnon) {
		return nil, fmt.Errorf("vm: boot answered %T, not an application struct", app)
	}
	fields := make(map[string]any, s.NumFields())
	var installed rt.Context
	found := false
	for i := range s.Desc.Fields {
		v := s.Field(i)
		fields[s.Desc.Fields[i].Name] = v
		if c, isContext := v.(rt.Context); isContext {
			installed, found = c, true
		}
	}
	rt.PublishScoped(root, fields)
	if found {
		// An app with a Context field makes it the active context; one with
		// none runs under the root context the frame already has.
		rt.InstallContext(root, installed)
	}
	booted := *m
	booted.hostFrame = root
	return &booted, nil
}

// isBootFunc reports whether f is a boot this program records, the program's
// or a `tests` group's. Only called for an activation leaving with deferred
// calls pending, so the resolution is not on any hot path.
func (m *Machine) isBootFunc(f *ir.Func) bool {
	if f == nil {
		return false
	}
	is := func(sym *ir.Symbol) bool {
		b, err := m.resolveFunc(sym, "boot")
		return err == nil && b == f
	}
	if m.boot != nil && is(m.boot) {
		return true
	}
	for sym := range m.testBoots {
		if is(sym) {
			return true
		}
	}
	return false
}

// deferToBootCleanup hands a boot's pending deferred calls to rt's boot
// cleanup instead of running them as boot answers: a `defer` in boot is the
// app's cleanup and runs when the app ends (the program exits, or a test case
// finishes), most recent first. Each runs on a
// fresh activation under the runtime frame it was registered in, since boot's
// own activation is gone by then.
func (m *Machine) deferToBootCleanup(fr *frame) {
	pending := fr.deferred
	fr.deferred = nil
	for _, d := range pending {
		d := d
		detached := &frame{fn: fr.fn, runtime: d.runtime, depth: m.depth}
		rt.DeferBootAt(d.runtime, d.call.Pos().Line(), func() {
			if _, err := m.invokeDeferred(detached, d); err != nil {
				panic(err)
			}
		})
	}
}

// bootFailed is a boot that answered the failing value of a `try`: the
// program's failure, reported as `boot failed: <value>`.
type bootFailed struct{ text string }

func (b *bootFailed) Error() string { return b.text }

// appField reads one published application field through the activation's
// scoped lineage.
func appField(fr *frame, n *ir.Ref) error {
	name := strings.TrimPrefix(n.Sym().Name(), "$")
	v, ok := rt.LookupScopedField(fr.runtime, name)
	if !ok {
		return fmt.Errorf("vm: %s: %s is not published; the program's boot has not run",
			fr.fn.Name(), n.Sym().Name())
	}
	fr.write(n.Dst(), v)
	return nil
}

// storeAppField enters one replacement for the rest of the activation. The
// producer retains a write only in a named function's own straight body, so
// the activation's end is its scope's end.
func storeAppField(fr *frame, n *ir.Store) error {
	if n.Kind() != ir.StoreAppField {
		return fmt.Errorf("vm: %s: this machine runs no %s", fr.fn.Name(), n)
	}
	v, err := fr.read(n.Src())
	if err != nil {
		return err
	}
	fr.runtime = rt.EnterScopedField(fr.runtime, strings.TrimPrefix(n.Sym().Name(), "$"), v)
	return nil
}

// scopeValue is the running scope a RefScope read: the activation's runtime
// frame, whose lineage holds the app fields, Context and deadline in force.
type scopeValue struct{ runtime *rt.Frame }

// OpaqueText makes the handle an rt.Opaque; no Nomi value holds one.
func (*scopeValue) OpaqueText() string { return "<scope>" }

// storeScope restores a scope RefScope read, ending the extent of the app
// field and Context writes made since, as leaving a block does. A
// deadline entered since stays on the activation's release list, which runs
// at its end.
func storeScope(fr *frame, n *ir.Store) error {
	v, err := fr.read(n.Src())
	if err != nil {
		return err
	}
	s, ok := v.(*scopeValue)
	if !ok {
		return fmt.Errorf("vm: %s: %s restores %T, not a scope", fr.fn.Name(), n, v)
	}
	fr.runtime = s.runtime
	return nil
}

// startupValue is the `Startup` boot receives: the process environment with
// the host's overrides appended, and args, snapshotted through rt.NewStartup.
func startupValue(args []string, hostEnv map[string]string) *rt.Record {
	environ := os.Environ()
	for key, val := range hostEnv {
		environ = append(environ, key+"="+val)
	}
	snapshot := rt.NewStartup(environ, args)
	pairs := make([]rt.MapEntry[any, any], 0)
	for _, item := range rt.MapEntries(snapshot.Env) {
		pairs = append(pairs, rt.MapEntry[any, any]{Key: item.Key, Val: item.Val})
	}
	argv := make([]any, len(args))
	for i, arg := range args {
		argv[i] = arg
	}
	return startupDesc.Make(mapOf(nil, pairs), listOf(argv))
}
