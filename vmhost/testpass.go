package vmhost

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/progcache"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// Stored passes: `nomi test` does not run again a case with no effects
// whose code has not changed since it passed, and does not load a file
// whose every case is such a case. How, and why that is safe, is the
// package doc's "Stored passes".

// RunPolicy says which of a file's cases Test runs when the program uses
// stored passes (UseStoredPasses).
type RunPolicy uint8

const (
	// RunChangedOrEffects runs every case but a pure one whose code is a
	// stored pass's: `nomi test`'s default.
	RunChangedOrEffects RunPolicy = iota
	// RunAll runs every case (`--all`).
	RunAll
	// RunChanged runs only the cases whose code no stored pass holds,
	// those with effects too (`--changed`).
	RunChanged
)

// passesKind names the record of a program's stored passes, beside its
// load's.
const passesKind = "passes"

// passScheme is the version of what a fingerprint holds. It moves whenever
// caseFingerprint's contents do; a record of another scheme reads as none.
const passScheme = 1

// UseStoredPasses makes Test read and write the stored passes of c's
// program under policy. c is the cache p was read from or kept in; a nil
// c, a program with a debugger, and a stdlib module's cases use none.
func (p *Program) UseStoredPasses(c *Cache, policy RunPolicy) {
	if c == nil || c.kind != loadKind || p.prog.Entry().Path != c.path {
		return
	}
	p.passes = &passStore{cache: c, policy: policy, image: p.kept}
}

// passStore is what Test reads and writes of a program's stored passes.
type passStore struct {
	cache  *Cache
	policy RunPolicy
	// image is the sum of the kept program's record (progcache.Entry.Sum)
	// this program was decoded from (Cache.Program), or zero for one
	// loaded or relinked: what a pass this run decides is verified on.
	image [32]byte
	entry *progcache.Entry
	// was is the record as read, now what Test will write.
	was, now passesRecord
	sums     *vm.CodeSums
}

// passesRecord is the record of a program's stored passes: each case's by
// name, and the kept program and the time limit its verified passes were
// decided on.
type passesRecord struct {
	image   [32]byte
	timeout time.Duration
	passes  map[string]storedPass
}

// storedPass is one case's stored pass.
type storedPass struct {
	sum   [32]byte
	count CaseCount
	parts []vm.CodePart
	// pure says the case's walk found no effect.
	pure bool
	// verified says sum was made, and matched or stored, from the
	// program its record's image names, under its record's timeout: what
	// lets a run answer the case with no program loaded (Cache.TestUnchanged).
	verified bool
}

// open reads the record, once, under the program's time limit. A pass
// verified on another program or under another limit keeps its
// fingerprint and is no longer verified.
func (s *passStore) open(timeout time.Duration) {
	if s.entry != nil {
		return
	}
	s.entry = s.cache.entry.Sibling(passesKind)
	s.was = decodePasses(s.entry.Payload())
	s.now = passesRecord{image: s.image, timeout: timeout, passes: make(map[string]storedPass, len(s.was.passes))}
	same := s.image != [32]byte{} && s.was.image == s.image && s.was.timeout == timeout
	for name, pass := range s.was.passes {
		pass.verified = pass.verified && same
		s.now.passes[name] = pass
	}
	s.sums = vm.NewCodeSums()
}

// caseFingerprint is the sum a stored pass of c is held to: the fingerprint
// scheme and the compiler's identity (so a new build of nomi, its std
// and its VM included, runs every case again), the case's name, the
// settings of its group and its run that change what it does (a virtual
// clock, the time it may take) and of its program (the locales whose data
// its machine reads), and its code, the parts of its walk: each function
// it can run, by key, with its sum without positions and the lines of its
// assertions whose failure is a value (vm.CodePart).
func (p *Program) caseFingerprint(c ir.TestCase, parts []vm.CodePart) [32]byte {
	h := sha256.New()
	var buf [binary.MaxVarintLen64]byte
	num := func(v int64) { h.Write(buf[:binary.PutVarint(buf[:], v)]) }
	str := func(s string) {
		num(int64(len(s)))
		h.Write([]byte(s))
	}
	num(passScheme)
	str(artifactcache.CompilerID())
	str(c.Name())
	if c.Group().VirtualClock {
		num(1)
	} else {
		num(0)
	}
	num(int64(p.testTimeout))
	num(int64(len(p.locales)))
	for _, l := range p.locales {
		str(l)
	}
	str(p.source)
	num(int64(len(parts)))
	for _, part := range parts {
		str(part.Key)
		h.Write(part.Sum[:])
		num(int64(len(part.Lines)))
		for _, l := range part.Lines {
			num(int64(l))
		}
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// decide is how a case named name, whose fingerprint is sum and whose walk
// is reach, is decided (one of rt's Decided constants): rt.DecidedUnchanged
// when a stored pass holds sum and the policy lets an unchanged case with
// reach's effects go unrun, with that pass; otherwise why it runs, with
// what a pass of it would store. Either pass is verified when this
// program is a kept one's (passStore.image).
func (s *passStore) decide(name string, sum [32]byte, reach vm.CaseReach) (storedPass, string) {
	verified := s.image != [32]byte{}
	pure := len(reach.Effects) == 0
	now := storedPass{sum: sum, parts: reach.Parts, pure: pure, verified: verified}
	pass, ok := s.was.passes[name]
	switch {
	case s.policy == RunAll:
		return now, rt.DecidedAll
	case !pure && s.policy != RunChanged:
		s.entry.Logf("%s: ran: it has effects (%s)", name, reach.Effects[0])
		return now, rt.DecidedEffects
	case !ok:
		s.entry.Logf("%s: ran: no stored pass", name)
		return now, rt.DecidedNew
	case pass.sum != sum:
		why, moved := partsDiffer(pass.parts, reach.Parts)
		s.entry.Logf("%s: ran: %s", name, why)
		if moved {
			return now, rt.DecidedMoved
		}
		return now, rt.DecidedChanged
	}
	s.entry.Logf("%s: unchanged", name)
	pass.pure, pass.verified = pure, verified
	return pass, rt.DecidedUnchanged
}

// partsDiffer says what differs between a stored pass's parts and a case's
// parts now, for NOMI_DEBUG_ARTIFACT_CACHE: the first function whose code
// changed, came or went, or the settings when the parts are the same.
// moved says only the lines of assertions whose failure is a value differ.
func partsDiffer(was, now []vm.CodePart) (why string, moved bool) {
	byKey := func(parts []vm.CodePart) map[string][]vm.CodePart {
		out := map[string][]vm.CodePart{}
		for _, p := range parts {
			out[p.Key] = append(out[p.Key], p)
		}
		return out
	}
	before, after := byKey(was), byKey(now)
	var keys []string
	for k := range after {
		keys = append(keys, k)
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	movedIn := ""
	for _, k := range keys {
		a, b := before[k], after[k]
		switch {
		case len(a) == 0:
			return fmt.Sprintf("its code now reaches %s", partName(k)), false
		case len(b) == 0:
			return fmt.Sprintf("its code no longer reaches %s", partName(k)), false
		case !slices.EqualFunc(a, b, func(x, y vm.CodePart) bool { return x.Sum == y.Sum }):
			return fmt.Sprintf("the code of %s changed", partName(k)), false
		case movedIn == "" && !slices.EqualFunc(a, b, func(x, y vm.CodePart) bool { return slices.Equal(x.Lines, y.Lines) }):
			movedIn = k
		}
	}
	if movedIn != "" {
		return fmt.Sprintf("an assertion in %s, whose failure is a value, moved", partName(movedIn)), true
	}
	return "its settings changed (its name, its group's clock, --timeout, the locales or the compiler)", false
}

// partName is a part's key as a reader reads it.
func partName(key string) string {
	return strings.TrimLeft(key, "\x00\x01")
}

// keep stores pass as c's, and forget drops c's stored pass.
func (s *passStore) keep(name string, pass storedPass) { s.now.passes[name] = pass }
func (s *passStore) forget(name string)                { delete(s.now.passes, name) }

// store writes the record when it changed, holding only the cases names
// lists, the file's cases now.
func (s *passStore) store(names []string) {
	if s.entry == nil {
		return
	}
	for name := range s.now.passes {
		if !slices.Contains(names, name) {
			delete(s.now.passes, name)
		}
	}
	data := encodePasses(s.now)
	if bytes.Equal(data, encodePasses(s.was)) {
		return
	}
	s.entry.Store(nil, nil, data)
}

// storedFiles counts the test files this process answered from their
// passes records (Cache.TestUnchanged).
var storedFiles atomic.Int64

// FilesAnsweredFromPassesForTest is how many test files this process
// answered from their passes records with no program loaded: what a test
// of stored passes reads to know which path a run took.
func FilesAnsweredFromPassesForTest() int64 { return storedFiles.Load() }

// TestUnchanged reports the cases opts selects in c's entry file, as
// Test reports them, from the program's passes record alone, when the
// kept program is a hit and every selected case has a pass verified on
// it under timeout that policy lets go unrun: one with no effects, or any
// under RunChanged. It answers whether it did; when it did not, the file
// loads and Test decides each case. opts are Load's; one Cache.Program
// would refuse makes it a miss.
//
// A verified pass's fingerprint was made from the program the kept
// record's payload decodes to, which is that payload's sum (passStore.image),
// under the time limit the passes record names. Every other input of the
// fingerprint is fixed by the kept record being a hit: the program's files'
// text, nomi.toml's locales among them, the compiler, which keys both
// records, and the scheme, which keys the passes record. So each case Test
// would decide from the program answers its fingerprint, and is decided
// unchanged. In a test binary the file is loaded as well and each case
// decided, and a case decided otherwise panics.
func (c *Cache) TestUnchanged(rep *TestReport, file string, opts TestOptions, label func(string) string, policy RunPolicy, timeout time.Duration, loadOpts ...Option) bool {
	if c == nil || c.kind != loadKind || policy == RunAll || irbuild.DeclinesPlanted() {
		return false
	}
	if cfg := newConfig(loadOpts); !cfg.cacheable() {
		return false
	}
	image, ok := c.entry.Sum()
	if !ok {
		return false
	}
	entry := c.entry.Sibling(passesKind)
	results, why := unchangedCases(c.path, decodePasses(entry.Payload()), image, opts, policy, timeout)
	if why != "" {
		entry.Logf("loaded: %s", why)
		return false
	}
	if testing.Testing() {
		c.verifyUnchanged(results, opts, policy, timeout, loadOpts)
	}
	entry.Logf("answered from the record: %d cases unchanged", len(results))
	storedFiles.Add(1)
	reportCases(rep, file, results, label)
	return true
}

// unchangedCases is TestUnchanged's answer from rec for the entry file at
// path: each case opts selects, as Test would answer it decided
// unchanged, or why a case is not answered so.
func unchangedCases(path string, rec passesRecord, image [32]byte, opts TestOptions, policy RunPolicy, timeout time.Duration) ([]CaseResult, string) {
	switch {
	case len(rec.passes) == 0:
		return nil, "no stored pass"
	case rec.image != image:
		return nil, "its passes were not decided on the kept program"
	case rec.timeout != timeout:
		return nil, fmt.Sprintf("its passes were decided under --timeout %s", rec.timeout)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err.Error()
	}
	nodes, err := parser.Parse(lexer.Lex(string(src)))
	if err != nil {
		return nil, "it does not parse"
	}
	plan, duplicates := frontend.SelectTests(nodes, opts)
	if len(duplicates) > 0 {
		return nil, "it declares a test name twice"
	}
	results := make([]CaseResult, len(plan))
	for i, tc := range plan {
		name := tc.FullName()
		pass, ok := rec.passes[name]
		switch {
		case !ok || !pass.verified:
			return nil, name + " has no verified pass"
		case !pass.pure && policy != RunChanged:
			return nil, name + " has effects"
		}
		results[i] = CaseResult{Name: name, Line: tc.Line, EndLine: tc.LastLine(),
			Cases: pass.count.Cases, Subtests: pass.count.Subtests, Decided: rt.DecidedUnchanged, Effects: !pass.pure}
	}
	return results, ""
}

// verifyUnchanged loads c's program and decides each case opts selects as
// Test does, and panics when one is not decided unchanged with the cases
// TestUnchanged answered for it: a test binary's check that a file
// answered from its passes record answers as the loaded file would.
func (c *Cache) verifyUnchanged(answered []CaseResult, opts TestOptions, policy RunPolicy, timeout time.Duration, loadOpts []Option) {
	p, ok := c.Program(loadOpts...)
	if !ok {
		panic(fmt.Sprintf("vmhost: %s was answered from its passes record, and its kept program does not load", c.path))
	}
	p.SetTestTimeout(timeout, "")
	p.UseStoredPasses(c, policy)
	got := p.entryCases(io.Discard, opts, p.passes)
	if len(got) != len(answered) {
		panic(fmt.Sprintf("vmhost: %s was answered from its passes record with %d cases, and its program decides %d", c.path, len(answered), len(got)))
	}
	for i, r := range got {
		if r.Decided != rt.DecidedUnchanged || r.Name != answered[i].Name || r.Count() != answered[i].Count() || r.Line != answered[i].Line || r.EndLine != answered[i].EndLine {
			panic(fmt.Sprintf("vmhost: %s :: %s was answered from its passes record as %+v, and its program decides it %q as %+v",
				c.path, answered[i].Name, answered[i].Count(), r.Decided, r.Count()))
		}
	}
}

const (
	passesMagic   = "NOMIPASS"
	passesVersion = 2
)

// Pass flags, a byte per case.
const (
	passSubtests = 1 << iota
	passPure
	passVerified
)

// encodePasses writes rec: the scheme, the image and the timeout, a table
// of the distinct parts, then each case's name, sum, flags, count and parts
// as indices into the table, by name.
func encodePasses(rec passesRecord) []byte {
	passes := rec.passes
	names := make([]string, 0, len(passes))
	for name := range passes {
		names = append(names, name)
	}
	slices.Sort(names)
	index := map[string]int{}
	var table []vm.CodePart
	partID := func(p vm.CodePart) string {
		return fmt.Sprintf("%s\x00%x\x00%v", p.Key, p.Sum, p.Lines)
	}
	for _, name := range names {
		for _, p := range passes[name].parts {
			if _, ok := index[partID(p)]; !ok {
				index[partID(p)] = len(table)
				table = append(table, p)
			}
		}
	}
	var b bytes.Buffer
	b.WriteString(passesMagic)
	putUvarint(&b, passesVersion)
	putUvarint(&b, passScheme)
	b.Write(rec.image[:])
	putUvarint(&b, uint64(rec.timeout))
	putUvarint(&b, uint64(len(table)))
	for _, p := range table {
		putString(&b, p.Key)
		b.Write(p.Sum[:])
		putUvarint(&b, uint64(len(p.Lines)))
		for _, l := range p.Lines {
			putUvarint(&b, uint64(l))
		}
	}
	putUvarint(&b, uint64(len(names)))
	for _, name := range names {
		pass := passes[name]
		putString(&b, name)
		b.Write(pass.sum[:])
		var flags byte
		if pass.count.Subtests {
			flags |= passSubtests
		}
		if pass.pure {
			flags |= passPure
		}
		if pass.verified {
			flags |= passVerified
		}
		b.WriteByte(flags)
		putUvarint(&b, uint64(pass.count.Cases))
		putUvarint(&b, uint64(len(pass.parts)))
		for _, p := range pass.parts {
			putUvarint(&b, uint64(index[partID(p)]))
		}
	}
	return b.Bytes()
}

// decodePasses reads what encodePasses wrote, or an empty record from one
// that is missing, does not read, or is of another version or scheme.
func decodePasses(data []byte) passesRecord {
	none := passesRecord{passes: map[string]storedPass{}}
	if !bytes.HasPrefix(data, []byte(passesMagic)) {
		return none
	}
	r := bytes.NewReader(data[len(passesMagic):])
	bad := false
	uv := func() uint64 {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			bad = true
		}
		return v
	}
	read := func(n uint64) []byte {
		if bad || n > uint64(r.Len()) {
			bad = true
			return nil
		}
		buf := make([]byte, n)
		_, _ = r.Read(buf)
		return buf
	}
	sum := func() (s [32]byte) {
		copy(s[:], read(32))
		return s
	}
	if uv() != passesVersion || uv() != passScheme || bad {
		return none
	}
	out := passesRecord{image: sum(), timeout: time.Duration(uv()), passes: map[string]storedPass{}}
	n := uv()
	if bad || n > uint64(r.Len()) {
		return none
	}
	table := make([]vm.CodePart, 0, n)
	for i := uint64(0); i < n && !bad; i++ {
		p := vm.CodePart{Key: string(read(uv())), Sum: sum()}
		lines := uv()
		if lines > uint64(r.Len()) {
			bad = true
			break
		}
		for j := uint64(0); j < lines; j++ {
			p.Lines = append(p.Lines, int(uv()))
		}
		table = append(table, p)
	}
	cases := uv()
	for i := uint64(0); i < cases && !bad; i++ {
		name := string(read(uv()))
		pass := storedPass{sum: sum()}
		var flags byte
		if f := read(1); len(f) == 1 {
			flags = f[0]
		}
		pass.count.Subtests = flags&passSubtests != 0
		pass.pure = flags&passPure != 0
		pass.verified = flags&passVerified != 0
		pass.count.Cases = int(uv())
		parts := uv()
		if parts > uint64(r.Len()) {
			bad = true
			break
		}
		for j := uint64(0); j < parts; j++ {
			k := uv()
			if k >= uint64(len(table)) {
				bad = true
				break
			}
			pass.parts = append(pass.parts, table[k])
		}
		out.passes[name] = pass
	}
	if bad || r.Len() != 0 {
		return none
	}
	return out
}
