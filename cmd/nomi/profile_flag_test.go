package main

import (
	"os"
	"slices"
	"testing"
)

// TestTakeProfileFlag checks that `nomi run --profile` sets NOMI_PROFILE
// and leaves the other flags, the file and the program's arguments in order.
func TestTakeProfileFlag(t *testing.T) {
	for _, c := range []struct {
		args, rest []string
		env        string
	}{
		{args: []string{"--profile", "main.nomi", "--profile"}, rest: []string{"main.nomi", "--profile"}, env: "1"},
		{args: []string{"--profile=run.pb.gz", "main.nomi"}, rest: []string{"main.nomi"}, env: "run.pb.gz"},
		{args: []string{"--watch", "--profile", "main.nomi", "x"}, rest: []string{"--watch", "main.nomi", "x"}, env: "1"},
		{args: []string{"main.nomi", "--profile"}, rest: []string{"main.nomi", "--profile"}, env: ""},
	} {
		t.Setenv("NOMI_PROFILE", "")
		rest := takeProfileFlag(c.args)
		if !slices.Equal(rest, c.rest) {
			t.Errorf("takeProfileFlag(%q) = %q, want %q", c.args, rest, c.rest)
		}
		if got := os.Getenv("NOMI_PROFILE"); got != c.env {
			t.Errorf("takeProfileFlag(%q) set NOMI_PROFILE to %q, want %q", c.args, got, c.env)
		}
	}
}
