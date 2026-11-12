package analysis_test

import "testing"

func TestBoundaryReturn_InferredValues(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"lambda value", `fn main()
    f = fn ->
        return 7
    end
    result = f()
    _ = result
end`, "Int"},
		{"lambda unit", `fn main()
    f = fn ->
        dbg 1
        return
    end
    result = f()
    _ = result
end`, "Unit"},
		{"both branches", `fn main()
  f = fn b: Bool ->
      if b then return 1 else return 2
  end
  result = f(True)
  _ = result
end`, "Int"},
		{"nested lambda", `fn main()
  f = fn ->
    inner = fn ->
        return "inner"
    end
    _ = inner()
    return 9
  end
  result = f()
  _ = result
end`, "Int"},
		{"nested named function", `fn main()
  f = fn ->
    fn inner() -> String
        return "inner"
    end
    _ = inner()
    return 9
  end
  result = f()
  _ = result
end`, "Int"},
		{"concurrent value", `fn main()
    result = concurrent
        return 7
    end
    _ = result
end`, "Int"},
		{"nested concurrent", `fn main()
  f = fn ->
      _ = concurrent
          return "inner"
      end
      return 9
  end
  result = f()
  _ = result
end`, "Int"},
		{"generic returned value", `fn identity<T>(value: T) -> T
  f = fn ->
      return value
  end
  f()
end
fn main()
    result = identity(7)
    _ = result
end`, "Int"},
		{"try before explicit result", `fn fallible() -> Result<Int, String>
    Ok(3)
end
fn main()
  f = fn ->
      value = fallible()?
      return Ok(value)
  end
  result = f()
  _ = result
end`, "Result<Int, String>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fa, errs := checkSourceWithStdlib(tc.src)
			expectClean(t, errs)
			for _, sym := range fa.Definitions {
				if sym.Name == "result" {
					if sym.Type == nil || sym.Type.String() != tc.want {
						t.Fatalf("result type = %v, want %s", sym.Type, tc.want)
					}
					return
				}
			}
			t.Fatal("result binding was not analyzed")
		})
	}
}

func TestBoundaryReturn_TryCannotEscapeScalarReturn(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn fallible() -> Result<Int, String>
    Ok(3)
end
fn main()
    f = fn ->
        value = fallible()?
        return value
    end
    _ = f()
end`)
	expectErrorContaining(t, errs, "`?` cannot propagate a Result out of a lambda whose result is Int")
}

func TestBoundaryReturn_ContextualInterface(t *testing.T) {
	for _, tail := range []string{"return Right{}", "Right{}"} {
		_, errs := checkSourceWithStdlib(`interface Value end
struct Left end
struct Right end
impl Value for Left end
impl Value for Right end
fn use(f: (Bool) -> Value) -> Value
  f(True)
end
fn main()
  _ = use(fn b ->
    if b then return Left{}
    ` + tail + `
  end)
end`)
		expectClean(t, errs)
	}
}

func TestBoundaryReturn_RejectsDisagreement(t *testing.T) {
	for _, src := range []string{
		`fn main()
    f = fn b: Bool ->
        (if b then return "bad")
        7
    end
    _ = f(False)
end`,
		`fn main()
    f = fn b: Bool ->
        (if b then return 7)
        return "bad"
    end
    _ = f(False)
end`,
		`fn main()
    f = fn b: Bool ->
        (if b then return)
        return 7
    end
    _ = f(False)
end`,
		`fn main()
    _ = concurrent
        (if True then return "bad")
        7
    end
end`,
		`fn use(f: (Bool) -> Int) -> Int
    f(True)
end
fn main()
    _ = use(fn b ->
        (if b then return 7)
        "bad"
    end)
end`,
	} {
		_, errs := checkSourceWithStdlib(src)
		expectErrorContaining(t, errs, "return type mismatch")
	}
}
