package stdhttp

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// When the program starts draining its supervisors, a listening server
// shuts down as Go's Server.Shutdown does: it stops accepting at once, and
// a request it already took runs to its end and is answered. Before
// rt.OnDrain the drain only cancelled tasks at the end of its budget, and
// a server kept accepting through the whole grace period.
func TestListen_DrainStopsAcceptingAndFinishesWhatItTook(t *testing.T) {
	defer rt.ResetSupervisors()
	durations := rt.Cons(int64(0), rt.Cons(int64(0), rt.Cons(int64(0), rt.Cons(int64(0), rt.Cons(int64(5*time.Second), (*rt.List[int64])(nil))))))
	flags := rt.Cons(true, rt.Cons(true, rt.Cons(true, (*rt.List[bool])(nil))))
	res := Listen("127.0.0.1:0", durations, nil, flags, nil, false, nil, nil, nil, false, nil, false, nil, false, nil, false)
	if res.Tag != 1 {
		t.Fatalf("Listen failed: %v", res.Err)
	}
	l := res.Ok
	ln := listenerOf(l)
	base := "http://" + Address(l)

	taken := make(chan *incoming)
	release := make(chan struct{})
	go func() {
		for {
			next := Next(nil, l)
			if next.Tag != 1 {
				close(taken)
				return
			}
			i := incomingOf(next.Some)
			taken <- i
			go func() {
				<-release
				i.w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(i.w, "finished")
				close(i.done)
			}()
		}
	}()

	type answer struct {
		status int
		body   string
		err    error
	}
	got := make(chan answer, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			got <- answer{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		got <- answer{status: resp.StatusCode, body: string(b)}
	}()
	<-taken // the server has handed the request to the program

	drained := make(chan struct{})
	go func() { rt.DrainSupervisors(); close(drained) }()

	// The server stops accepting as the drain starts, while the request it
	// took is still unanswered.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := c.Get(base + "/late")
		if err != nil {
			break
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			t.Fatal("the server still accepted a connection after the drain started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(release)
	a := <-got
	if a.err != nil || a.status != http.StatusOK || a.body != "finished" {
		t.Fatalf("the request taken before the drain got %+v, want 200 \"finished\"", a)
	}
	if _, open := <-taken; open {
		t.Fatal("Next handed over another request after shutdown")
	}
	<-ln.closed
	<-drained
}
