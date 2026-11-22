package reactor

import (
	"slices"
	"testing"
	"time"
)

// The options nomi_load_with and nomi_call_with read: each key the export
// takes, and an error naming what is wrong with anything else.
func TestParseOptions(t *testing.T) {
	ok := []struct {
		data string
		want callOptions
	}{
		{"", callOptions{}},
		{"{}", callOptions{}},
		{`{"std": []}`, callOptions{std: []string{}}},
		{`{"host": ["lookup"]}`, callOptions{host: []string{"lookup"}}},
		{`{"std": ["std::io", "std::fs"], "host": [], "timeout_ms": 250}`, callOptions{std: []string{"std::io", "std::fs"}, host: []string{}, timeout: 250 * time.Millisecond}},
		{`{"host_calls": ["greet", "helpers/util.shout"]}`, callOptions{hostCalls: []string{"greet", "helpers/util.shout"}}},
		{`{"host_calls": []}`, callOptions{hostCalls: []string{}}},
	}
	for _, c := range ok {
		got, err := parseOptions("nomi_load_with", []byte(c.data), keyStd, keyHost, keyHostCalls, keyTimeout)
		if err != nil {
			t.Errorf("%s: %v", c.data, err)
			continue
		}
		if (got.std == nil) != (c.want.std == nil) || !slices.Equal(got.std, c.want.std) ||
			(got.host == nil) != (c.want.host == nil) || !slices.Equal(got.host, c.want.host) ||
			(got.hostCalls == nil) != (c.want.hostCalls == nil) || !slices.Equal(got.hostCalls, c.want.hostCalls) || got.timeout != c.want.timeout {
			t.Errorf("%s: %+v, want %+v", c.data, got, c.want)
		}
	}
	bad := []struct{ data, want string }{
		{`{"allow": []}`, `nomi_load_with has no option "allow"; its options are "std", "host", "host_calls" and "timeout_ms"`},
		{`[]`, `nomi_load_with: the options must be one JSON object, such as {"std": ["std::io"], "host": ["lookup"], "host_calls": ["lookup"], "timeout_ms": 500}`},
		{`{} {}`, `nomi_load_with: the options must be one JSON object, such as {"std": ["std::io"], "host": ["lookup"], "host_calls": ["lookup"], "timeout_ms": 500}`},
		{`{"std": null}`, `nomi_load_with: "std" must be an array of std names, such as ["std::io"]; [] allows nothing`},
		{`{"std": [1]}`, `nomi_load_with: "std" must be an array of std names, such as ["std::io"]; [] allows nothing`},
		{`{"std": ["host:lookup"]}`, `nomi_load_with: "std" holds std names, and host:lookup is a host function; list its key, "lookup", in "host"`},
		{`{"host": "lookup"}`, `nomi_load_with: "host" must be an array of host function keys, such as ["lookup"]; [] allows none`},
		{`{"host": ["host:lookup"]}`, `nomi_load_with: "host" lists keys as a host table registers them: write "lookup", not "host:lookup"`},
		{`{"host_calls": "greet"}`, `nomi_load_with: "host_calls" must be an array of host function keys, such as ["lookup"]; [] answers none`},
		{`{"host_calls": ["host:greet"]}`, `nomi_load_with: "host_calls" lists keys as a host table registers them: write "greet", not "host:greet"`},
		{`{"timeout_ms": 0}`, `nomi_load_with: "timeout_ms" must be a whole number of milliseconds above 0, such as 500, not 0`},
		{`{"timeout_ms": 1.5}`, `nomi_load_with: "timeout_ms" must be a whole number of milliseconds above 0, such as 500, not 1.5`},
		{`{"timeout_ms": "500"}`, `nomi_load_with: "timeout_ms" must be a whole number of milliseconds above 0, such as 500, not "500"`},
	}
	for _, c := range bad {
		_, err := parseOptions("nomi_load_with", []byte(c.data), keyStd, keyHost, keyHostCalls, keyTimeout)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v\nwant %s", c.data, err, c.want)
		}
	}
	if _, err := parseOptions("nomi_call_with", []byte(`{"std": []}`), keyTimeout); err == nil ||
		err.Error() != `nomi_call_with has no option "std"; its options are "timeout_ms"` {
		t.Errorf("a call's std: %v", err)
	}
}
