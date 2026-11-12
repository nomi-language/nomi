package analysis_test

import (
	"strings"
	"testing"
)

// An impl's own type parameters are rigid when its functions' signatures are
// compared with the interface's: inside `impl Store<String, T> for Cache<T>`,
// `T` is one fixed unknown type and matches only `T`. Function-level type
// parameters may be renamed, but each interface one must correspond to its
// own impl one.

const rigidStoreDecls = `interface Store<K, V>
    fn get(store: self, key: K) -> Maybe<V>
    fn put(store: self, key: K, value: V) -> self
end

struct Cache<T>
    items: Map<String, T>
end

`

const rigidMapperDecls = `interface Named
    fn name(n: self) -> String
end

interface Mapper<T>
    fn apply<U>(m: self, f: (T) -> U) -> List<U>
    fn both<U, W>(m: self, f: (T) -> U, g: (T) -> W) -> (List<U>, List<W>)
    fn pick<U>(m: self, x: U, y: U) -> U where U: Equatable
end

struct Bag<T>
    items: List<T>
end

`

// mapperImpl is an `impl Mapper<T> for Bag<T>` block with the given three
// functions.
func mapperImpl(apply, both, pick string) string {
	return rigidMapperDecls + "impl Mapper<T> for Bag<T>\n" + apply + "\n" + both + "\n" + pick + "end\n"
}

const (
	goodApply = "    fn apply<V>(b: Bag<T>, f: (T) -> V) -> List<V>\n        b.items |> Iter.map(f) |> Iter.to_list()\n    end\n"
	goodBoth  = "    fn both<X, Y>(b: Bag<T>, f: (T) -> X, g: (T) -> Y) -> (List<X>, List<Y>)\n        (b.items |> Iter.map(f) |> Iter.to_list(), b.items |> Iter.map(g) |> Iter.to_list())\n    end\n"
	goodPick  = "    fn pick<U>(_b: Bag<T>, x: U, y: U) -> U where U: Equatable\n        if x == y then x else y\n    end\n"
)

func TestImplRigidTypeParams_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{
			"return type names a concrete type where the interface has the impl's parameter",
			rigidStoreDecls + "impl Store<String, T> for Cache<T>\n    fn get(cache: Cache<T>, key: String) -> Maybe<Int>\n        _ = Map.get(cache.items, key)\n        None\n    end\n\n    fn put(cache: Cache<T>, key: String, value: T) -> Cache<T>\n        Cache{items: Map.put(cache.items, key, value)}\n    end\nend\n",
			[]string{"impl function 'get': return type Maybe<Int> does not match interface 'Store' return type Maybe<T>"},
		},
		{
			"parameter type names a concrete type where the interface has the impl's parameter",
			rigidStoreDecls + "impl Store<String, T> for Cache<T>\n    fn get(cache: Cache<T>, key: String) -> Maybe<T>\n        Map.get(cache.items, key)\n    end\n\n    fn put(cache: Cache<T>, key: String, value: Int) -> Cache<T>\n        _ = (key, value)\n        cache\n    end\nend\n",
			[]string{"impl function 'put': parameter 3 has type Int, but interface 'Store' declares T"},
		},
		{
			"two impl parameters swapped",
			"interface Pair<A, B>\n    fn first(p: self) -> A\nend\n\nstruct Two<A, B>\n    a: A\n    b: B\nend\n\nimpl Pair<A, B> for Two<A, B>\n    fn first(t: Two<A, B>) -> B\n        t.b\n    end\nend\n",
			[]string{"impl function 'first': return type B does not match interface 'Pair' return type A"},
		},
		{
			"nested generic with a concrete argument",
			rigidStoreDecls + "impl Store<String, List<T>> for Cache<T>\n    fn get(cache: Cache<T>, key: String) -> Maybe<List<Int>>\n        _ = (cache, key)\n        None\n    end\n\n    fn put(cache: Cache<T>, key: String, value: List<T>) -> Cache<T>\n        _ = (key, value)\n        cache\n    end\nend\n",
			[]string{"impl function 'get': return type Maybe<List<Int>> does not match interface 'Store' return type Maybe<List<T>>"},
		},
		{
			"function-level parameter fixed to a concrete type",
			mapperImpl("    fn apply(b: Bag<T>, f: (T) -> Int) -> List<Int>\n        b.items |> Iter.map(f) |> Iter.to_list()\n    end\n", goodBoth, goodPick),
			[]string{"impl function 'apply': parameter 2 has type (T) -> Int, but interface 'Mapper' declares (T) -> U", "impl function 'apply': return type List<Int> does not match interface 'Mapper' return type List<U>"},
		},
		{
			"function-level parameter replaced by the impl's parameter",
			mapperImpl("    fn apply(b: Bag<T>, f: (T) -> T) -> List<T>\n        b.items |> Iter.map(f) |> Iter.to_list()\n    end\n", goodBoth, goodPick),
			[]string{"impl function 'apply': parameter 2 has type (T) -> T, but interface 'Mapper' declares (T) -> U", "impl function 'apply': return type List<T> does not match interface 'Mapper' return type List<U>"},
		},
		{
			"two function-level parameters merged",
			mapperImpl(goodApply, "    fn both<X>(b: Bag<T>, f: (T) -> X, g: (T) -> X) -> (List<X>, List<X>)\n        (b.items |> Iter.map(f) |> Iter.to_list(), b.items |> Iter.map(g) |> Iter.to_list())\n    end\n", goodPick),
			[]string{"impl function 'both': parameter 3 has type (T) -> X, but interface 'Mapper' declares (T) -> W", "impl function 'both': return type (List<X>, List<X>) does not match interface 'Mapper' return type (List<U>, List<W>)"},
		},
		{
			"where bound the interface function does not carry",
			mapperImpl(goodApply, goodBoth, "    fn pick<U>(_b: Bag<T>, x: U, y: U) -> U where U: Equatable and Named\n        if x == y then x else y\n    end\n"),
			[]string{"impl function 'pick': `where U: Named` is not required by interface 'Mapper', and a call through the interface does not check it"},
		},
		{
			"where bound on the impl's parameter",
			"interface Named\n    fn name(n: self) -> String\nend\n\ninterface Show<T>\n    fn show(s: self, x: T) -> String\nend\n\nstruct Cache<T>\n    item: T\nend\n\nimpl Show<T> for Cache<T>\n    fn show(_s: Cache<T>, x: T) -> String where T: Named\n        Named.name(x)\n    end\nend\n",
			[]string{"impl function 'show': `where T: Named` is not required by interface 'Show', and a call through the interface does not check it; put it on the impl block (`impl ... where T: Named`)"},
		},
		{
			"header without arguments binds the interface parameter once per block",
			"interface Holder<T>\n    fn hold(h: self) -> T\n    fn put(h: self, x: T) -> self\nend\n\nstruct Shelf<T>\n    item: T\nend\n\nimpl Holder for Shelf<T>\n    fn hold(s: Shelf<T>) -> T\n        s.item\n    end\n\n    fn put(h: Shelf<T>, x: Int) -> Shelf<T>\n        _ = x\n        h\n    end\nend\n",
			[]string{"impl function 'put': parameter 2 has type Int, but interface 'Holder' declares T"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Message
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("errors = %v, want exactly %q", errs, tc.want)
			}
		})
	}
}

func TestImplRigidTypeParams_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{
			"the impl's parameter where the interface has it",
			rigidStoreDecls + "impl Store<String, T> for Cache<T>\n    fn get(cache: Cache<T>, key: String) -> Maybe<T>\n        Map.get(cache.items, key)\n    end\n\n    fn put(cache: Cache<T>, key: String, value: T) -> Cache<T>\n        Cache{items: Map.put(cache.items, key, value)}\n    end\nend\n",
		},
		{
			"nested generic over the impl's parameter",
			rigidStoreDecls + "impl Store<String, List<T>> for Cache<T>\n    fn get(cache: Cache<T>, key: String) -> Maybe<List<T>>\n        _ = (cache, key)\n        None\n    end\n\n    fn put(cache: Cache<T>, key: String, value: List<T>) -> Cache<T>\n        _ = (key, value)\n        cache\n    end\nend\n",
		},
		{
			"two impl parameters in place",
			"interface Pair<A, B>\n    fn first(p: self) -> A\n    fn second(p: self) -> B\nend\n\nstruct Two<A, B>\n    a: A\n    b: B\nend\n\nimpl Pair<A, B> for Two<A, B>\n    fn first(t: Two<A, B>) -> A\n        t.a\n    end\n\n    fn second(t: Two<A, B>) -> B\n        t.b\n    end\nend\n",
		},
		{
			"function-level parameters renamed, and the interface's where bound",
			mapperImpl(goodApply, goodBoth, goodPick),
		},
		{
			"function-level parameters renamed in swapped order",
			mapperImpl(goodApply, "    fn both<Y, X>(b: Bag<T>, f: (T) -> X, g: (T) -> Y) -> (List<X>, List<Y>)\n        (b.items |> Iter.map(f) |> Iter.to_list(), b.items |> Iter.map(g) |> Iter.to_list())\n    end\n", goodPick),
		},
		{
			"where bound on the impl's parameter, written on the block",
			"interface Named\n    fn name(n: self) -> String\nend\n\ninterface Show<T>\n    fn show(s: self, x: T) -> String\nend\n\nstruct Cache<T>\n    item: T\nend\n\nimpl Show<T> for Cache<T> where T: Named\n    fn show(_s: Cache<T>, x: T) -> String where T: Named\n        Named.name(x)\n    end\nend\n",
		},
		{
			"where bound on the impl's parameter, required by the interface function",
			"interface Named\n    fn name(n: self) -> String\nend\n\ninterface Show<T>\n    fn show(s: self, x: T) -> String where T: Named\nend\n\nstruct Cache<T>\n    item: T\nend\n\nimpl Show<T> for Cache<T>\n    fn show(_s: Cache<T>, x: T) -> String where T: Named\n        Named.name(x)\n    end\nend\n",
		},
		{
			"header without arguments, used consistently",
			"interface Holder<T>\n    fn hold(h: self) -> T\n    fn put(h: self, x: T) -> self\nend\n\nstruct Shelf<T>\n    item: T\nend\n\nimpl Holder for Shelf<T>\n    fn hold(s: Shelf<T>) -> T\n        s.item\n    end\n\n    fn put(_h: Shelf<T>, x: T) -> Shelf<T>\n        Shelf{item: x}\n    end\nend\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			for _, e := range errs {
				if strings.HasPrefix(e.Message, "impl function") {
					t.Fatalf("rejected: %v", errs)
				}
			}
			expectNoStdlibErrors(t, errs)
		})
	}
}
