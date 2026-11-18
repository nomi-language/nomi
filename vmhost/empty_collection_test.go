package vmhost_test

import "testing"

// An empty collection literal has the kind of the type the checker gave it
// from the start (irbuild's emptyOf), in every position it can stand: an
// annotated binding, an argument, a result, a branch, a generic function's
// body at two instances, a lambda's result, and a position whose element is
// an interface. Each prints as the empty collection it is and answers the
// operations its element type admits.
func TestRun_EmptyCollectionsHaveTheirRecordedKind(t *testing.T) {
	got := runSourceOutput(t, `import std::io

fn count_list(xs: List<Int>) -> Int
    List.length(xs)
end

fn count_set(s: Set<String>) -> Int
    Set.size(s)
end

fn count_vector(v: Vector<Float>) -> Int
    Vector.length(v)
end

fn count_map(m: Map<String, Int>) -> Int
    Map.size(m)
end

fn no_list() -> List<String>
    []
end

fn no_set() -> Set<Int>
    []
end

fn no_vector() -> Vector<Int>
    Vector[]
end

fn no_map() -> Map<Int, String>
    {}
end

fn pick(n: Int) -> List<Int>
    if n > 0 then [n] else []
end

fn pick_set(n: Int) -> Set<Int>
    case n
        0 -> Set[]
        _ -> Set[n]
    end
end

fn pick_map(n: Int) -> Map<Int, Int>
    if n > 0 then {n => n} else {}
end

fn grow<T>(_x: T) -> (List<T>, Set<T>, Vector<T>, Map<T, Int>)
    ([], [], [], {})
end

fn shown(xs: List<Display>) -> String
    xs |> Iter.map(Display.to_string) |> String.join(",")
end

fn main()
    xs: List<Int> = []
    s: Set<String> = []
    v: Vector<Float> = []
    m: Map<String, Int> = {}
    io.print("bindings ${List.length(xs)} ${Set.size(s)} ${Vector.length(v)} ${Map.size(m)}")
    io.print("named ${Set.size(Set[])} ${Vector.length(Vector[])}")
    io.print("arguments ${count_list([])} ${count_set([])} ${count_vector([])} ${count_map({})}")
    dbg no_list()
    dbg no_set()
    dbg no_vector()
    dbg no_map()
    dbg pick(0)
    dbg pick(2)
    dbg pick_set(0)
    dbg pick_map(0)
    dbg grow(1)
    dbg grow("a")
    make_list = fn n: Int -> if n > 0 then [n] else []
    make_set = fn n: Int -> if n > 0 then Set[n] else Set[]
    make_map = fn n: Int -> if n > 0 then {"k" => n} else {}
    dbg make_list(0)
    dbg make_set(0)
    dbg make_map(0)
    io.print("display [${shown([])}] [${shown([1, 2])}]")
    dbg [[], [1]]
    dbg (1, [], Set[], {})
    dbg [] == no_list()
    dbg Set.union(no_set(), Set[3])
end
`)
	want := `bindings 0 0 0 0
named 0 0
arguments 0 0 0 0
dbg line 66: no_list() = []
dbg line 67: no_set() = Set[]
dbg line 68: no_vector() = Vector[]
dbg line 69: no_map() = {}
dbg line 70: pick(0) = []
dbg line 71: pick(2) = [2]
dbg line 72: pick_set(0) = Set[]
dbg line 73: pick_map(0) = {}
dbg line 74: grow(1) = ([], Set[], Vector[], {})
dbg line 75: grow("a") = ([], Set[], Vector[], {})
dbg line 79: make_list(0) = []
dbg line 80: make_set(0) = Set[]
dbg line 81: make_map(0) = {}
display [] [1,2]
dbg line 83: [[], [1]] = [[], [1]]
dbg line 84: (1, [], Set[], {}) = (1, [], Set[], {})
dbg line 85: [] == no_list() = True
dbg line 86: Set.union(no_set(), Set[3]) = Set[3]
`
	if got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}
