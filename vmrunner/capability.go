package vmrunner

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// WithCapabilities holds the program to names: what it may reach outside
// the machine, named as vmhost.WithCapabilities and nomi.toml's
// [capabilities] std lists name it (`std::fs`, `std::io`, `std::startup`),
// and `host:<key>` for a crossing into a table passed with WithHosts. A
// compiled image records no Go import path, so a `go` binding is named by
// its key, `host:<key>`, and a `go:` name is refused. With no names the
// program may reach nothing outside itself. Without this option nothing is
// held.
//
// The load (Start, Boot, LoadProgram) or run refuses the program before it
// runs anything, with a *CapabilityError naming each call, when an outside
// call is reachable from anything a host can run: main and the boot, and
// each exported function with its argument decoders and result encoder.
// The walk is vmhost's (internal/vm's reach walk), over the decoded image,
// and as conservative: a call in a branch that never runs counts.
//
// A name listed twice, a name of no form a list knows, a `go:` name and a
// `host:` key no table answers fail the load. A `std::` name is checked for
// its form only: the runner links no list of std modules, so a misspelled
// module allows nothing rather than failing the load.
func WithCapabilities(names ...string) Option {
	return func(c *config) {
		c.lists = append(c.lists, heldList{via: "vmrunner.WithCapabilities", names: append([]string{}, names...), std: true, host: true})
	}
}

// WithCapabilitiesFrom is WithCapabilities for a host whose lists reach
// the runner another way, as the wasip1 reactor's nomi_load_with options
// do: std, when not nil, holds every call but a host function's to std
// names, and stdVia names that list in errors; host, when not nil, holds
// the host functions to the keys it lists, written without `host:`, and
// hostVia names that list. With std given, a nil host allows no host
// function, as WithCapabilities' list naming none does; with std nil, the
// calls other than host functions are not held.
func WithCapabilitiesFrom(stdVia string, std []string, hostVia string, host []string) Option {
	if std != nil && host == nil {
		host = []string{}
	}
	return func(c *config) {
		if std != nil {
			c.lists = append(c.lists, heldList{via: stdVia, names: append([]string{}, std...), std: true})
		}
		if host != nil {
			names := make([]string, len(host))
			for i, key := range host {
				names[i] = "host:" + key
			}
			c.lists = append(c.lists, heldList{via: hostVia, names: names, host: true, keys: true})
		}
	}
}

// heldList is one list a host holds the program to: names, passed as via,
// holding the host functions when host is set and every other call when
// std is set. keys says the host wrote its host names as bare keys.
type heldList struct {
	via       string
	names     []string
	std, host bool
	keys      bool
}

// holds reports whether l holds a call named name.
func (l heldList) holds(name string) bool {
	if strings.HasPrefix(name, "host:") {
		return l.host
	}
	return l.std
}

// Edge is how the reach walk got from one function to the next
// (vm.EdgeKind), as vmhost.Edge.
type Edge = vm.EdgeKind

// ChainStep is one function on the way from a root to an outside call, and
// the Edge that reached it.
type ChainStep = vm.ChainStep

// OutsideCall is one outside call a held program reaches that a list does
// not allow.
type OutsideCall struct {
	// Name is the call's name (internal/capability's rule): `std::` and the
	// std module the code called, or `host:` and a crossing's key.
	Name string
	// Site is the call's file and line as the image records them.
	Site string
	// Callee is what the code at Site calls: a std function, or the
	// crossing itself.
	Callee string
	// Crossing is a crossing into Go the call leads to, or "" for a boot's
	// Startup.
	Crossing string
	// Edge is how the code at Site reaches Callee.
	Edge Edge
	// Chain is the functions from a root to the one holding the call,
	// each with the Edge that reached it, outermost first.
	Chain []ChainStep
	// Allowed is the host's list that does not allow the call.
	Allowed []string
	// list is that list, for the help.
	list heldList
}

// CapabilityError is a program refused because it reaches outside calls
// the host's lists (WithCapabilities) do not allow. Its text is one
// refusal per call, in vmhost's words, at the file and line the image
// records.
type CapabilityError struct {
	// Calls is each call a list does not allow, in position order.
	Calls []OutsideCall
}

func (e *CapabilityError) Error() string {
	var b strings.Builder
	for i, c := range e.Calls {
		if i > 0 {
			b.WriteString("\n")
		}
		add := c.Name
		if c.list.keys {
			add = strings.TrimPrefix(add, "host:")
		}
		b.WriteString(c.Site + ": " + capability.Message("this program reaches", c.Name, "the host", "", c.Allowed))
		b.WriteString("\n" + c.Site + ": help: " + vm.ReachText(c.Chain, c.Edge, c.Callee))
		b.WriteString("\n" + c.Site + ": help: " + capability.HostAddHint(add, c.list.via) + ", or " + capability.RemoveHint(c.Edge == vm.EdgeBoot))
	}
	return b.String()
}

// hold refuses the program in im when cfg holds it to lists
// (WithCapabilities) and a list is wrong, or the program reaches an
// outside call a list that holds it does not allow from roots and boots.
// hosts are the tables the runner binds; compiler is std/compiler's, or
// nil.
func (cfg *config) hold(im ir.Image, entry *ir.Module, roots []*ir.Func, boots []*ir.Symbol, hosts []HostTable, compiler HostTable) error {
	if len(cfg.lists) == 0 {
		return nil
	}
	for _, l := range cfg.lists {
		err := capability.CheckHostList(l.via, "vmrunner.WithHosts", "a compiled program records no Go import path and names a Go binding by its key, as host:<key>", l.names,
			func() (map[string]bool, error) { return hostadapt.TableKeys(hosts...) }, nil)
		if err != nil {
			return err
		}
	}
	if len(roots) == 0 && len(boots) == 0 {
		return nil
	}
	// std/compiler's crossings are named std::compiler, as vmhost names
	// them; the runner does not know which of them reach outside, so it
	// counts all of them.
	var other func(key string) (bool, string)
	if compiler != nil {
		keys, err := hostadapt.TableKeys(compiler)
		if err != nil {
			return err
		}
		other = func(key string) (bool, string) {
			if keys[key] {
				return true, "compiler"
			}
			return true, ""
		}
	}
	var refused []OutsideCall
	for _, c := range vm.ReachedOutsideCalls(entry, im.Modules, roots, vm.EdgeRoot, boots, other) {
		name := capability.Name(c.Std, "", hostadapt.DisplayName(c.Crossing))
		for _, l := range cfg.lists {
			if !l.holds(name) || capability.Allows(l.names, nil, name, "") {
				continue
			}
			refused = append(refused, OutsideCall{
				Name:     name,
				Site:     rt.DisplayPath(c.At.File()) + ":" + strconv.Itoa(c.At.Line()),
				Callee:   hostadapt.DisplayName(c.Callee),
				Crossing: hostadapt.DisplayName(c.Crossing),
				Edge:     c.Edge,
				Chain:    c.Chain,
				Allowed:  l.names,
				list:     l,
			})
			break
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return &CapabilityError{Calls: refused}
}
