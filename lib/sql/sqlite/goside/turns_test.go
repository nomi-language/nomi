package goside

import (
	"context"
	"sync"
	"testing"
	"time"
)

// The tests of how tasks share one Conn: a statement sent through the
// connection while another task's transaction is open waits for it to end,
// and never runs inside it.

func openCounted(t *testing.T) *Conn {
	t.Helper()
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(c) })
	if a := Exec(background, c, 0, 0, []string{"CREATE TABLE t (n INTEGER)"}, nil); a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	return c
}

func insertN(ctx context.Context, c *Conn, depth, token, n int64) Failure {
	return Exec(ctx, c, depth, token, []string{"INSERT INTO t VALUES (", ")"}, []Cell{{Kind: KindInt, Int: n}}).Failure
}

func count(t *testing.T, c *Conn) int64 {
	t.Helper()
	a := Run(background, c, 0, 0, []string{"SELECT count(*) FROM t"}, nil)
	if a.Failure.Kind != FailNone {
		t.Fatal(a.Failure)
	}
	return a.Rows[0][0].Int
}

// While one goroutine holds a transaction open, a read and a write sent
// through the connection from others wait for it rather than fail, and
// run after it: the read sees what the transaction committed, and the
// write is not part of it.
func TestConn_StatementsOutsideATransactionWaitForIt(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback"} {
		t.Run(outcome, func(t *testing.T) {
			c := openCounted(t)
			tx := Begin(background, c, 0, 0)
			if tx.Failure.Kind != FailNone {
				t.Fatal(tx.Failure)
			}
			if f := insertN(background, c, tx.Depth, tx.Token, 1); f.Kind != FailNone {
				t.Fatal(f)
			}
			var wg sync.WaitGroup
			var read Answer
			var wrote Failure
			done := make(chan string, 2)
			wg.Add(2)
			go func() {
				defer wg.Done()
				read = Run(context.Background(), c, 0, 0, []string{"SELECT count(*) FROM t"}, nil)
				done <- "read"
			}()
			go func() {
				defer wg.Done()
				wrote = insertN(context.Background(), c, 0, 0, 2)
				done <- "write"
			}()
			select {
			case which := <-done:
				t.Fatalf("the %s outside the transaction ran while it was open", which)
			case <-time.After(100 * time.Millisecond):
			}
			var f Failure
			if outcome == "commit" {
				f = Commit(c, tx.Depth, tx.Token)
			} else {
				f = Rollback(c, tx.Depth, tx.Token)
			}
			if f.Kind != FailNone {
				t.Fatal(f)
			}
			wg.Wait()
			if wrote.Kind != FailNone {
				t.Errorf("the write outside the transaction answered %+v", wrote)
			}
			if read.Failure.Kind != FailNone {
				t.Fatalf("the read outside the transaction answered %+v", read.Failure)
			}
			// The transaction's row stands only if it committed; the
			// outside write stands either way.
			want := int64(1)
			if outcome == "commit" {
				want = 2
			}
			if got := count(t, c); got != want {
				t.Errorf("%d rows after the %s, want %d", got, outcome, want)
			}
			if n := read.Rows[0][0].Int; n < want-1 || n > want {
				t.Errorf("the read outside saw %d rows", n)
			}
		})
	}
}

// A statement waiting for another task's transaction stops when its own
// context ends, as a Timeout, and the transaction is unharmed.
func TestConn_AWaitingStatementStopsWithItsContext(t *testing.T) {
	c := openCounted(t)
	tx := Begin(background, c, 0, 0)
	if f := insertN(background, c, tx.Depth, tx.Token, 1); f.Kind != FailNone {
		t.Fatal(f)
	}
	ctx, cancel := context.WithTimeout(background, 50*time.Millisecond)
	defer cancel()
	if f := insertN(ctx, c, 0, 0, 2); f.Kind != FailTimeout {
		t.Errorf("a write waiting past its deadline answered %+v", f)
	}
	if l := Begin(ctx, c, 0, 0); l.Failure.Kind != FailTimeout {
		t.Errorf("a begin waiting past its deadline answered %+v", l)
	}
	if f := Commit(c, tx.Depth, tx.Token); f.Kind != FailNone {
		t.Fatal(f)
	}
	if got := count(t, c); got != 1 {
		t.Errorf("%d rows, want 1", got)
	}
}

// Many goroutines running transactions and plain statements on one Conn
// all succeed, and each transaction is all or nothing.
func TestConn_ManyTasksShareOneConn(t *testing.T) {
	c := openCounted(t)
	var wg sync.WaitGroup
	failures := make(chan Failure, 400)
	for i := range int64(100) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			switch i % 4 {
			case 0, 1:
				// Commits two rows, or rolls both back.
				tx := Begin(ctx, c, 0, 0)
				if tx.Failure.Kind != FailNone {
					failures <- tx.Failure
					return
				}
				for _, n := range []int64{i, i} {
					if f := insertN(ctx, c, tx.Depth, tx.Token, n); f.Kind != FailNone {
						failures <- f
					}
				}
				time.Sleep(time.Millisecond)
				if i%4 == 0 {
					if f := Commit(c, tx.Depth, tx.Token); f.Kind != FailNone {
						failures <- f
					}
				} else if f := Rollback(c, tx.Depth, tx.Token); f.Kind != FailNone {
					failures <- f
				}
			case 2:
				if f := insertN(ctx, c, 0, 0, i); f.Kind != FailNone {
					failures <- f
				}
			case 3:
				if a := Run(ctx, c, 0, 0, []string{"SELECT count(*) FROM t"}, nil); a.Failure.Kind != FailNone {
					failures <- a.Failure
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Errorf("failure: %+v", f)
	}
	// 25 committed transactions of two rows, 25 plain inserts.
	if got := count(t, c); got != 75 {
		t.Errorf("%d rows, want 75", got)
	}
	odd := Run(background, c, 0, 0, []string{"SELECT count(*) FROM t WHERE n % 4 = 1"}, nil)
	if odd.Rows[0][0].Int != 0 {
		t.Errorf("a rolled-back transaction's rows stand: %+v", odd.Rows)
	}
}

// Closing the connection while a transaction is open rolls it back and
// gives its turn back, so a statement waiting for it answers Closed.
func TestConn_CloseReleasesAWaitingStatement(t *testing.T) {
	c := openCounted(t)
	tx := Begin(background, c, 0, 0)
	got := make(chan Failure)
	go func() { got <- insertN(context.Background(), c, 0, 0, 1) }()
	time.Sleep(50 * time.Millisecond)
	if f := Close(c); f.Kind != FailNone {
		t.Fatal(f)
	}
	select {
	case f := <-got:
		if f.Kind != FailClosed {
			t.Errorf("the waiting statement answered %+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting statement still waits after the close")
	}
	if f := Commit(c, tx.Depth, tx.Token); f.Kind != FailClosed {
		t.Errorf("commit after the close answered %+v", f)
	}
}
