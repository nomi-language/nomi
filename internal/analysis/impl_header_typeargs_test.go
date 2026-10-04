package analysis_test

import (
	"strings"
	"testing"
)

const storeDecls = `interface Store<K, V> {
    fn get(store: self, key: K): Maybe<V>
    fn put(store: self, key: K, value: V): self
}

struct Cache<T> {
    items: Map<String, T>
}

`

// implErrors returns the messages of src's errors that start with one of
// the per-impl conformance prefixes.
func implErrors(src string) []string {
	_, errs := checkSourceWithStdlib(src)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e.Message, "impl ") {
			out = append(out, e.Message)
		}
	}
	return out
}

// A header that spells the interface's type arguments with the block's own
// type parameter (`Store<String, T>`) gets every per-impl check the bare
// header (`Store`) gets.
func TestImplHeaderTypeArgs_ConformanceChecksRun(t *testing.T) {
	for name, tc := range map[string]struct {
		block string
		want  []string
	}{
		"missing functions": {
			block: "impl Store<String, T> for Cache<T> {}\n",
			want: []string{
				"impl 'Store' for 'Cache': missing function 'get' required by interface 'Store'",
				"impl 'Store' for 'Cache': missing function 'put' required by interface 'Store'",
			},
		},
		"parameter type": {
			block: "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: Int): Maybe<T> {\n        _ = key\n        Map.get(cache.items, \"k\")\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n        Cache{items: Map.put(cache.items, key, value)}\n    }\n}\n",
			want:  []string{"impl function 'get': parameter 2 has type Int, but interface 'Store' declares String"},
		},
		"return type": {
			block: "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Int {\n        _ = (cache, key)\n        1\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n        Cache{items: Map.put(cache.items, key, value)}\n    }\n}\n",
			want:  []string{"impl function 'get': return type Int does not match interface 'Store' return type Maybe<T>"},
		},
		"parameter count": {
			block: "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Maybe<T> {\n        Map.get(cache.items, key)\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T, more: Int): Cache<T> {\n        _ = more\n        Cache{items: Map.put(cache.items, key, value)}\n    }\n}\n",
			want:  []string{"impl function 'put' takes 4 parameters, but interface 'Store' declares 3"},
		},
		"parameter name": {
			block: "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Maybe<T> {\n        Map.get(cache.items, key)\n    }\n\n    fn put(cache: Cache<T>, k: String, value: T): Cache<T> {\n        Cache{items: Map.put(cache.items, k, value)}\n    }\n}\n",
			want:  []string{"impl function 'put': parameter name 'k' does not match interface declaration 'key'"},
		},
		"extra function": {
			block: "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Maybe<T> {\n        Map.get(cache.items, key)\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n        Cache{items: Map.put(cache.items, key, value)}\n    }\n\n    fn size(cache: Cache<T>): Int {\n        Map.size(cache.items)\n    }\n}\n",
			want:  []string{"impl 'Store' for 'Cache': function 'size' is not part of the interface"},
		},
		"complete": {
			block: "impl Store<String, T> for Cache<T> {\n    fn get(cache: Cache<T>, key: String): Maybe<T> {\n        Map.get(cache.items, key)\n    }\n\n    fn put(cache: Cache<T>, key: String, value: T): Cache<T> {\n        Cache{items: Map.put(cache.items, key, value)}\n    }\n}\n",
		},
	} {
		got := implErrors(storeDecls + tc.block)
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
