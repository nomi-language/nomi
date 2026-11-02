package stdprocess

import (
	"bytes"
	"slices"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// Fake is `process_testing.run`'s recording stand-in for the operating
// system: it starts nothing. Each start is answered by the first answer
// whose program and arguments match, and is recorded with the directory,
// the environment additions and the input the child was given. A start no
// answer matches fails as a missing program does, with NotFound.
type Fake struct {
	mu      sync.Mutex
	answers []answer
	calls   []*call
}

// answer is one canned reply: the child exits with code after writing
// stdout and stderr, or fails to start with failure.
type answer struct {
	program string
	args    []string
	anyArgs bool
	code    int64
	stdout  []byte
	stderr  []byte
	failure *rt.IOError
}

// call is one start the fake answered.
type call struct {
	program string
	args    []string
	dir     string
	env     []rt.MapEntry[string, string]
	mu      sync.Mutex
	input   bytes.Buffer
}

// NewFake is std/process_host's `new_fake`: a fake with no answers.
func NewFake() rt.ProcessFake { return rt.NewProcessFake(&Fake{}) }

func fakeOf(f rt.ProcessFake) *Fake {
	fake, ok := f.Fake().(*Fake)
	if !ok {
		rt.Trap("process_testing: the value is not a fake")
	}
	return fake
}

func listOf(l *rt.List[string]) []string {
	var out []string
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

func stringList(items []string) *rt.List[string] {
	var out *rt.List[string]
	for i := len(items) - 1; i >= 0; i-- {
		out = rt.Cons(items[i], out)
	}
	return out
}

// FakeAnswer is std/process_host's `fake_answer`: a start of program with
// args (with any arguments when anyArgs is set) exits with code after
// writing stdout and stderr.
func FakeAnswer(f rt.ProcessFake, program string, args *rt.List[string], anyArgs bool, code int64, stdout, stderr rt.Bytes) rt.Unit {
	fake := fakeOf(f)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.answers = append(fake.answers, answer{program: program, args: listOf(args), anyArgs: anyArgs,
		code: code, stdout: []byte(stdout), stderr: []byte(stderr)})
	return rt.Unit{}
}

// FakeRefuse is std/process_host's `fake_refuse`: a start of program with
// args (any when anyArgs is set) fails with e.
func FakeRefuse(f rt.ProcessFake, program string, args *rt.List[string], anyArgs bool, e rt.IOError) rt.Unit {
	fake := fakeOf(f)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.answers = append(fake.answers, answer{program: program, args: listOf(args), anyArgs: anyArgs, failure: &e})
	return rt.Unit{}
}

// FakeCalls is std/process_host's `fake_calls`: how many starts the fake
// answered.
func FakeCalls(f rt.ProcessFake) int64 {
	fake := fakeOf(f)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return int64(len(fake.calls))
}

func (fake *Fake) callAt(i int64) *call {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if i < 0 || i >= int64(len(fake.calls)) {
		rt.Trap("process_testing: no such call")
	}
	return fake.calls[i]
}

// FakeCommand is std/process_host's `fake_command`: the program and then
// the arguments of the call at position i, from 0.
func FakeCommand(f rt.ProcessFake, i int64) *rt.List[string] {
	c := fakeOf(f).callAt(i)
	return stringList(append([]string{c.program}, c.args...))
}

// FakeDir is std/process_host's `fake_dir`: the directory the call at
// position i asked for, or "" for the current one.
func FakeDir(f rt.ProcessFake, i int64) string { return fakeOf(f).callAt(i).dir }

// FakeEnv is std/process_host's `fake_env`: the environment the call at
// position i added.
func FakeEnv(f rt.ProcessFake, i int64) rt.Map[string, string] {
	return rt.MapOf(rt.HashString, rt.Eq[string], fakeOf(f).callAt(i).env)
}

// FakeInput is std/process_host's `fake_input`: everything the call at
// position i was given on its standard input.
func FakeInput(f rt.ProcessFake, i int64) rt.Bytes {
	c := fakeOf(f).callAt(i)
	c.mu.Lock()
	defer c.mu.Unlock()
	return rt.Bytes(c.input.String())
}

// start answers s from the first matching answer, and records it.
func (fake *Fake) start(fr *rt.Frame, s spec) (*child, *rt.IOError) {
	rec := &call{program: s.program, args: s.args, dir: s.dir, env: s.env}
	fake.mu.Lock()
	fake.calls = append(fake.calls, rec)
	var found *answer
	for i := range fake.answers {
		a := &fake.answers[i]
		if a.program == s.program && (a.anyArgs || slices.Equal(a.args, s.args)) {
			found = a
			break
		}
	}
	fake.mu.Unlock()
	if found == nil {
		return nil, &rt.IOError{Tag: rt.TagIONotFound, Path: s.program}
	}
	if found.failure != nil {
		e := *found.failure
		return nil, &e
	}

	c := &child{program: s.program, finished: true, status: []int64{0, found.code}}
	switch s.stdin {
	case stdinReader:
		if r := rt.ReaderReadAll(fr, s.input); r.Tag == rt.TagOk {
			rec.input.WriteString(string(r.Ok))
		} else {
			return nil, &r.Err
		}
	case stdinInherit:
		if r := rt.ReaderReadAll(fr, rt.IOStdin()); r.Tag == rt.TagOk {
			rec.input.WriteString(string(r.Ok))
		} else {
			return nil, &r.Err
		}
	case stdinPipe:
		c.stdin = rt.NewWriter(rt.WriteSink{Name: "<" + s.program + " stdin>", W: lockedWriter{rec}, Unbuffered: true})
		c.piped[0] = true
	}
	for i, mode := range []int64{s.out, s.err} {
		data := found.stdout
		if i == 1 {
			data = found.stderr
		}
		switch mode {
		case outInherit:
			if len(data) > 0 {
				_, _ = frameOutput{fr: fr, errors: i == 1}.Write(data)
			}
		case outCapture:
			c.captured[i] = bytes.NewBuffer(slices.Clone(data))
		case outPipe:
			reader := rt.ReaderFromBytes(rt.Bytes(data))
			if i == 0 {
				c.stdout = reader
			} else {
				c.stderr = reader
			}
			c.piped[1+i] = true
		}
	}
	return c, nil
}

// lockedWriter appends what a fake child's standard input is given to its
// call's record.
type lockedWriter struct{ c *call }

func (w lockedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	return w.c.input.Write(p)
}
