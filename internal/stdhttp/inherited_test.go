package stdhttp

import (
	"reflect"
	"testing"
)

// The descriptors a process is started with are read as systemd's
// sd_listen_fds_with_names reads them: none unless LISTEN_PID names this
// process, "unknown" for each when nothing names them, and an error for a
// variable that does not parse or names a different count.
// TestInheritedSockets_* (cmd/nomi) passes real sockets to a child.
func TestListenFDNames_ReadsTheProtocol(t *testing.T) {
	for _, c := range []struct {
		what  string
		env   map[string]string
		names []string
		err   string
	}{
		{"none passed", map[string]string{}, nil, ""},
		{"another process's", map[string]string{"LISTEN_PID": "7", "LISTEN_FDS": "2"}, nil, ""},
		{"unnamed", map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2"}, []string{"unknown", "unknown"}, ""},
		{"named", map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2", "LISTEN_FDNAMES": "web:stats"}, []string{"web", "stats"}, ""},
		{"no count", map[string]string{"LISTEN_PID": "42"}, nil, "LISTEN_FDS is not a count: "},
		{"no process", map[string]string{"LISTEN_PID": "me", "LISTEN_FDS": "1"}, nil, "LISTEN_PID is not a process id: me"},
		{"names miscounted", map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2", "LISTEN_FDNAMES": "web"}, nil, "LISTEN_FDNAMES names 1 descriptors and LISTEN_FDS passes 2"},
	} {
		names, err := listenFDNames(func(k string) string { return c.env[k] }, 42)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != c.err || !reflect.DeepEqual(names, c.names) {
			t.Errorf("%s: got %q, %q; want %q, %q", c.what, names, got, c.names, c.err)
		}
	}
}
