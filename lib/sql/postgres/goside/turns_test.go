//go:build !js

package goside

import (
	"context"
	"testing"
	"time"
)

// While one goroutine holds a transaction open, a read and a write sent
// through the Conn from others never fail: on a pool they run at once on
// other connections, and on one connection they wait for the transaction
// to end and run outside it.
func TestSession_StatementsOutsideATransactionNeverFail(t *testing.T) {
	url := fresh(t)
	for _, kind := range []string{"pool", "connect"} {
		t.Run(kind, func(t *testing.T) {
			s := Pool(url)
			if kind == "connect" {
				var err error
				if s, err = Connect(background, url); err != nil {
					t.Fatal(err)
				}
			}
			defer Close(s)
			table := "outside_" + kind
			exec_(t, s, "CREATE TABLE "+table+" (n INTEGER)")
			tx, f := Begin(background, s)
			if f.Kind != FailNone {
				t.Fatal(f)
			}
			exec_(t, tx, "INSERT INTO "+table+" VALUES (1)")
			read := make(chan Answer, 1)
			wrote := make(chan Failure, 1)
			go func() { read <- Run(context.Background(), s, []string{"SELECT count(*) FROM " + table}, nil) }()
			go func() {
				wrote <- Exec(context.Background(), s, []string{"INSERT INTO " + table + " VALUES (2)"}, nil).Failure
			}()
			if kind == "pool" {
				// It sees the outside write or not, never the transaction's.
				if a := <-read; a.Failure.Kind != FailNone || a.Rows[0][0].Int > 1 {
					t.Errorf("the read beside the transaction answered %+v", a)
				}
				if f := <-wrote; f.Kind != FailNone {
					t.Errorf("the write beside the transaction answered %+v", f)
				}
			} else {
				select {
				case <-read:
					t.Fatal("the read ran while the connection's transaction was open")
				case <-wrote:
					t.Fatal("the write ran while the connection's transaction was open")
				case <-time.After(100 * time.Millisecond):
				}
			}
			if f := Rollback(background, tx); f.Kind != FailNone {
				t.Fatal(f)
			}
			if kind == "connect" {
				if a := <-read; a.Failure.Kind != FailNone {
					t.Errorf("the read after the transaction answered %+v", a)
				}
				if f := <-wrote; f.Kind != FailNone {
					t.Errorf("the write after the transaction answered %+v", f)
				}
			}
			// The rollback undid the transaction's row, not the outside write.
			got := Run(background, s, []string{"SELECT n FROM " + table}, nil)
			if got.Failure.Kind != FailNone || len(got.Rows) != 1 || got.Rows[0][0].Int != 2 {
				t.Errorf("rows after the rollback: %+v", got)
			}
		})
	}
}

// A statement waiting for the connection's transaction stops when its own
// context ends, as a Timeout.
func TestConnect_AWaitingStatementStopsWithItsContext(t *testing.T) {
	s := connect(t)
	tx, f := Begin(background, s)
	if f.Kind != FailNone {
		t.Fatal(f)
	}
	ctx, cancel := context.WithTimeout(background, 50*time.Millisecond)
	defer cancel()
	if f := Exec(ctx, s, []string{"SELECT 1"}, nil).Failure; f.Kind != FailTimeout {
		t.Errorf("a statement waiting past its deadline answered %+v", f)
	}
	if f := Rollback(background, tx); f.Kind != FailNone {
		t.Fatal(f)
	}
}
