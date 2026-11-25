package main

import "testing"

// Inputs as files: what an input reads of earlier inputs' files.

// A generic function an earlier input declared is instantiated at the types
// a later input calls it with, which no earlier lowering made.
func TestReplVM_AnEarlierGenericIsInstantiatedLater(t *testing.T) {
	replExpect(t, "fn pair<T>(x: T) -> (T, T)\n  (x, x)\nend\npair(1)\npair(\"s\")\n"+
		"struct Box<T>\n  v: T\nend\nb = Box{v: [1, 2]}\nb\nBox{v: pair(True)}\n",
		"(1, 1)\n(\"s\", \"s\")\nBox{v: [1, 2]}\nBox{v: (True, True)}\n")
}

// An impl redefined in a later input replaces the earlier one for the
// inputs after it, and an interface declared in one input is implemented
// and called in others.
func TestReplVM_ARedefinedImplReplacesTheEarlierOne(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"one ${p.x}\"\n  end\nend\n\"${P{x: 1}}\"\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"two ${p.x}\"\n  end\nend\n\"${P{x: 2}}\"\n"+
		"impl P\n  fn get(p: P) -> Int\n    p.x\n  end\nend\nP.get(P{x: 3})\n"+
		"impl P\n  fn get(p: P) -> Int\n    p.x * 10\n  end\nend\nP.get(P{x: 3})\n"+
		"interface Shape\n  fn area(s: self) -> Int\nend\n"+
		"impl Shape for P\n  fn area(p: P) -> Int\n    p.x * p.x\n  end\nend\n"+
		"fn total(s: Shape) -> Int\n  Shape.area(s)\nend\ntotal(P{x: 4})\n",
		"\"one 1\"\n\"two 2\"\n3\n30\n16\n")
}

// An input that fails after replacing an impl puts the earlier impl back.
func TestReplVM_AFailedRedefinitionKeepsTheEarlierImpl(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"one ${p.x}\"\n  end\nend\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    missing_name\n  end\nend\n\"${P{x: 1}}\"\n",
		"\"one 1\"\n",
		"undefined variable 'missing_name'")
}

// An enum and its variants carry between inputs.
func TestReplVM_AnEnumCarriesBetweenInputs(t *testing.T) {
	replExpect(t, "enum Color\n  Red\n  Green\nend\nc = Color.Green\nc\nc == Color.Red\n"+
		"fn name(c: Color) -> String\n  case c\n    Color.Red -> \"red\"\n    Color.Green -> \"green\"\n  end\nend\nname(c)\n",
		"Green\nFalse\n\"green\"\n")
}

// A `fn boot` runs with every input after it, so a later input, and a
// function a later input declares, reads its application fields.
func TestReplVM_LaterInputsRunUnderAnEarlierBoot(t *testing.T) {
	replExpect(t, "struct App\n  n: Int\nend\nfn boot() -> Result<App, String>\n  Ok(App{n: 3})\nend\n"+
		"App.n\nfn twice() -> Int\n  App.n * 2\nend\ntwice()\nx = 5\nx + App.n\n",
		"3\n6\n8\n")
}

// A derive in a later input implements the interface for an earlier
// input's type, for the inputs after it.
func TestReplVM_ALaterInputDerivesForAnEarlierType(t *testing.T) {
	replExpect(t, "struct Pt\n  x: Int\nend\nderive Comparable for Pt\n"+
		"[Pt{x: 2}, Pt{x: 1}] |> Iter.sort() |> Iter.to_list()\n"+
		"derive Hashable for Pt\nm = {Pt{x: 1} => \"one\"}\nMap.get(m, Pt{x: 1})\n",
		"[Pt{x: 1}, Pt{x: 2}]\nSome(\"one\")\n")
}

// A redefined impl replaces the earlier one for code earlier inputs
// compiled too: a function that called the old impl's function, directly,
// through string interpolation or dispatched on an interface value it
// already called it through, calls the new one.
func TestReplVM_ARedefinedImplReachesEarlierFunctions(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"one ${p.x}\"\n  end\nend\n"+
		"fn show(p: P) -> String\n  \"${p}\"\nend\nshow(P{x: 1})\n"+
		"impl Display for P\n  fn to_string(p: P) -> String\n    \"two ${p.x}\"\n  end\nend\nshow(P{x: 1})\n"+
		"impl P\n  fn get(p: P) -> Int\n    p.x\n  end\nend\n"+
		"fn use_get(p: P) -> Int\n  P.get(p)\nend\nuse_get(P{x: 3})\n"+
		"impl P\n  fn get(p: P) -> Int\n    p.x * 10\n  end\nend\nuse_get(P{x: 3})\n"+
		"interface Shape\n  fn area(s: self) -> Int\nend\n"+
		"impl Shape for P\n  fn area(p: P) -> Int\n    p.x\n  end\nend\n"+
		"fn total(s: Shape) -> Int\n  Shape.area(s) + 1\nend\ntotal(P{x: 4})\n"+
		"area_of = fn s: Shape -> Shape.area(s) + 2\np = P{x: 4}\narea_of(p)\n"+
		"impl Shape for P\n  fn area(p: P) -> Int\n    p.x * 10\n  end\nend\ntotal(P{x: 4})\narea_of(p)\n",
		"\"one 1\"\n\"two 1\"\n3\n30\n5\n6\n41\n42\n")
}

// An earlier function that a small impl function was compiled into (the
// VM's inlining) calls the redefined one: get's body is in the code of
// use_get and of twice, which g runs, and redefining get recompiles them
// with the new body in its place. The calls are not tail calls, which are
// never compiled in, and g's is a call through a value, which runs the
// code twice was compiled to before the redefinition.
func TestReplVM_ARedefinedFunctionCompiledIntoAnEarlierOneIsReplaced(t *testing.T) {
	replExpect(t, "struct P\n  x: Int\nend\n"+
		"impl P\n  fn get(p: P) -> Int\n    p.x\n  end\nend\n"+
		"fn use_get(p: P) -> Int\n  P.get(p) + 1\nend\n"+
		"fn twice(p: P) -> Int\n  use_get(p) * 2\nend\ng = twice\ng(P{x: 3})\n"+
		"impl P\n  fn get(p: P) -> Int\n    p.x * 10\n  end\nend\ng(P{x: 3})\nuse_get(P{x: 3})\n",
		"8\n62\n31\n")
}

// A derive in a later input for an earlier input's type replaces the impl
// the type had, for every derivable interface: the Debug every type has
// with no derive, a hand-written impl, and an earlier derive. Code an
// earlier input compiled calls the new impl too.
func TestReplVM_ALaterDeriveReplacesTheTypesImpl(t *testing.T) {
	replExpect(t, "import std::json.{Json, ToJson, FromJson}\n"+
		"struct R\n  x: Int\nend\nfn show(r: R) -> String\n  Debug.inspect(r)\nend\n"+
		"derive Debug for R\nR{x: 5}\n[R{x: 6}]\n"+
		"impl Debug for R\n  fn inspect(r: R) -> String\n    \"custom ${r.x}\"\n  end\nend\nshow(R{x: 7})\n"+
		"derive Debug for R\nshow(R{x: 8})\n"+
		"impl Display for R\n  fn to_string(r: R) -> String\n    \"hand ${r.x}\"\n  end\nend\n\"${R{x: 1}}\"\n"+
		"derive Display for R\n\"${R{x: 2}}\"\n"+
		"impl Equatable for R\n  fn equal(_: R, _: R) -> Bool\n    True\n  end\nend\n"+
		"fn same(a: R, b: R) -> Bool\n  a == b\nend\nsame(R{x: 1}, R{x: 2})\n"+
		"derive Equatable for R\nsame(R{x: 1}, R{x: 2})\n"+
		"derive Comparable for R\nR{x: 1} < R{x: 2}\nderive Comparable for R\nR{x: 3} < R{x: 2}\n"+
		"derive Hashable for R\nMap.get({R{x: 1} => 1}, R{x: 1})\nderive Hashable for R\nMap.get({R{x: 2} => 2}, R{x: 2})\n"+
		"derive ToJson for R\nJson.encode_as(R{x: 1})\nderive ToJson for R\nJson.encode_as(R{x: 2})\n"+
		"derive FromJson for R\nJson.decode_as<R>(\"{\\\"x\\\": 3}\")\n"+
		"enum Coin\n  Penny\n  Nickel\nend\nderive Variants for Coin\nCoin.variants()\n",
		"R{x: 5}\n[R{x: 6}]\n\"custom 7\"\n\"R{x: 8}\"\n\"hand 1\"\n\"R{x: 2}\"\nTrue\nFalse\n"+
			"True\nFalse\nSome(1)\nSome(2)\n\"{\\\"x\\\":1}\"\n\"{\\\"x\\\":2}\"\nOk(R{x: 3})\n[Penny, Nickel]\n")
}

// A rendering an earlier input compiled while a type's Debug was its shape
// renders through the Debug impl a later input writes for the type, and
// through the derive that replaces it, wherever the value sits: alone, in
// a List, as another struct's field, in an Option, and under a `dbg`. The
// `dbg` names the line of traced's input it was typed on, though traced's
// file imports R above it.
func TestReplVM_ALaterDebugImplReachesEarlierRenderings(t *testing.T) {
	replExpect(t, "struct R\n  x: Int\nend\nstruct S\n  r: R\nend\n"+
		"fn show(r: R) -> String\n  Debug.inspect(r)\nend\n"+
		"fn parts(rs: List<R>, s: S, m: Option<R>) -> String\n  \"${Debug.inspect(rs)} ${Debug.inspect(s)} ${Debug.inspect(m)}\"\nend\n"+
		"fn traced(r: R) -> R\n  dbg r\nend\n"+
		"show(R{x: 1})\n"+
		"impl Debug for R\n  fn inspect(r: R) -> String\n    \"custom ${r.x}\"\n  end\nend\n"+
		"show(R{x: 2})\nparts([R{x: 3}], S{r: R{x: 4}}, Some(R{x: 5}))\n_ = traced(R{x: 6})\n"+
		"derive Debug for R\nshow(R{x: 7})\nparts([R{x: 8}], S{r: R{x: 9}}, Some(R{x: 10}))\n",
		"\"R{x: 1}\"\n\"custom 2\"\n\"[custom 3] S{r: custom 4} Some(custom 5)\"\ndbg line 2: r = custom 6\n"+
			"\"R{x: 7}\"\n\"[R{x: 8}] S{r: R{x: 9}} Some(R{x: 10})\"\n")
}

// A derive that replaces a written Debug writes no function, and the
// renderings earlier inputs compiled print the type's shape from then on:
// one compiled before any impl and one compiled while the written impl was
// in force, which names its body, alone, in a List, as a field, in an
// Option and under a `dbg`. A written impl after the derive is the type's
// Debug again for both. The `dbg` names the line of traced's input it was
// typed on, though traced's file imports R above it.
func TestReplVM_ADeriveAfterAWrittenDebugReachesEarlierRenderings(t *testing.T) {
	parts := "\"${Debug.inspect(r)} ${Debug.inspect([r])} ${Debug.inspect(S{r:})} ${Debug.inspect(Some(r))}\""
	replExpect(t, "struct R\n  x: Int\nend\nstruct S\n  r: R\nend\n"+
		"fn early(r: R) -> String\n  "+parts+"\nend\n"+
		"impl Debug for R\n  fn inspect(r: R) -> String\n    \"one ${r.x}\"\n  end\nend\n"+
		"fn later(r: R) -> String\n  "+parts+"\nend\n"+
		"fn traced(r: R) -> R\n  dbg r\nend\n"+
		"early(R{x: 1})\nlater(R{x: 1})\n_ = traced(R{x: 1})\n"+
		"derive Debug for R\nearly(R{x: 2})\nlater(R{x: 2})\n_ = traced(R{x: 2})\n"+
		"impl Debug for R\n  fn inspect(r: R) -> String\n    \"two ${r.x}\"\n  end\nend\n"+
		"early(R{x: 3})\nlater(R{x: 3})\n_ = traced(R{x: 3})\n",
		"\"one 1 [one 1] S{r: one 1} Some(one 1)\"\n\"one 1 [one 1] S{r: one 1} Some(one 1)\"\ndbg line 2: r = one 1\n"+
			"\"R{x: 2} [R{x: 2}] S{r: R{x: 2}} Some(R{x: 2})\"\n\"R{x: 2} [R{x: 2}] S{r: R{x: 2}} Some(R{x: 2})\"\ndbg line 2: r = R{x: 2}\n"+
			"\"two 3 [two 3] S{r: two 3} Some(two 3)\"\n\"two 3 [two 3] S{r: two 3} Some(two 3)\"\ndbg line 2: r = two 3\n")
}

// A derive that replaces a written Display writes no function, and the
// renderings an earlier input compiled while the written impl was in force
// print the type's shape from then on, alone, in a List, as a derived
// type's field and in an Option. One compiled while the derive was in
// force names no body for the type, and a written impl after the derive
// is the type's Display again for both.
func TestReplVM_ADeriveAfterAWrittenDisplayReachesEarlierRenderings(t *testing.T) {
	parts := "\"${r} ${[r]} ${S{r:}} ${Some(r)}\""
	replExpect(t, "struct R\n  x: Int\nend\nstruct S\n  r: R\nend\n"+
		"impl Display for R\n  fn to_string(r: R) -> String\n    \"one ${r.x}\"\n  end\nend\n"+
		"derive Display for S\n"+
		"fn early(r: R) -> String\n  "+parts+"\nend\n"+
		"early(R{x: 1})\n"+
		"derive Display for R\n"+
		"fn later(r: R) -> String\n  "+parts+"\nend\n"+
		"early(R{x: 2})\nlater(R{x: 2})\n"+
		"impl Display for R\n  fn to_string(r: R) -> String\n    \"two ${r.x}\"\n  end\nend\n"+
		"early(R{x: 3})\nlater(R{x: 3})\n",
		"\"one 1 [one 1] S{r: one 1} Some(one 1)\"\n"+
			"\"R{x: 2} [R{x: 2}] S{r: R{x: 2}} Some(R{x: 2})\"\n\"R{x: 2} [R{x: 2}] S{r: R{x: 2}} Some(R{x: 2})\"\n"+
			"\"two 3 [two 3] S{r: two 3} Some(two 3)\"\n\"two 3 [two 3] S{r: two 3} Some(two 3)\"\n")
}

// A derive that replaces a written Equatable writes no function, and the
// comparisons an earlier input compiled while the written impl was in
// force compare the type by its shape from then on: `==`, a List, a
// struct field, an Option, `Equatable.equal` and a Set's elements. A
// written impl after the derive is the type's equality again for both.
func TestReplVM_ADeriveAfterAWrittenEquatableReachesEarlierComparisons(t *testing.T) {
	parts := "\"${a == b} ${[a] == [b]} ${S{r: a} == S{r: b}} ${Some(a) == Some(b)} ${Equatable.equal(a, b)} ${Set.size(Set[a, b])}\""
	replExpect(t, "struct R\n  x: Int\nend\nstruct S\n  r: R\nend\n"+
		"impl Equatable for R\n  fn equal(a: R, b: R) -> Bool\n    a.x % 10 == b.x % 10\n  end\nend\n"+
		"fn early(a: R, b: R) -> String\n  "+parts+"\nend\n"+
		"early(R{x: 1}, R{x: 11})\n"+
		"derive Equatable for R\n"+
		"fn later(a: R, b: R) -> String\n  "+parts+"\nend\n"+
		"early(R{x: 1}, R{x: 11})\nlater(R{x: 1}, R{x: 11})\n"+
		"impl Equatable for R\n  fn equal(a: R, b: R) -> Bool\n    a.x > 0 and b.x > 0\n  end\nend\n"+
		"early(R{x: 1}, R{x: 11})\nlater(R{x: 1}, R{x: 11})\n",
		"\"True True True True True 1\"\n"+
			"\"False False False False False 2\"\n\"False False False False False 2\"\n"+
			"\"True True True True True 1\"\n\"True True True True True 1\"\n")
}

// A derive that replaces a written Hashable writes no function, and the
// hashes an earlier input compiled while the written impl was in force
// hash the type by its shape from then on: `Hashable.hash`, `R.hash`, a
// List, a struct field, an Option and a value held as Hashable. The
// written impl gives every value one hash, and the shape tells R{x: 1}
// from R{x: 2}. A written impl after the derive is the type's hash again
// for both.
func TestReplVM_ADeriveAfterAWrittenHashableReachesEarlierHashes(t *testing.T) {
	parts := "\"${Hashable.hash(a) == Hashable.hash(b)} ${R.hash(a) == R.hash(b)} ${Hashable.hash([a]) == Hashable.hash([b])} " +
		"${Hashable.hash(S{r: a}) == Hashable.hash(S{r: b})} ${Hashable.hash(Some(a)) == Hashable.hash(Some(b))} ${held(a) == held(b)}\""
	replExpect(t, "struct R\n  x: Int\nend\nstruct S\n  r: R\nend\n"+
		"impl Hashable for R\n  fn hash(_r: R) -> Int\n    7\n  end\nend\n"+
		"fn held(h: Hashable) -> Int\n  Hashable.hash(h)\nend\n"+
		"fn early(a: R, b: R) -> String\n  "+parts+"\nend\n"+
		"early(R{x: 1}, R{x: 2})\n"+
		"derive Hashable for R\n"+
		"fn later(a: R, b: R) -> String\n  "+parts+"\nend\n"+
		"early(R{x: 1}, R{x: 2})\nlater(R{x: 1}, R{x: 2})\n"+
		"impl Hashable for R\n  fn hash(_r: R) -> Int\n    9\n  end\nend\n"+
		"early(R{x: 1}, R{x: 2})\nlater(R{x: 1}, R{x: 2})\n",
		"\"True True True True True True\"\n"+
			"\"False False False False False False\"\n\"False False False False False False\"\n"+
			"\"True True True True True True\"\n\"True True True True True True\"\n")
}

// An undefined name's hint offers every name the session has in scope, not
// only the ones the input's file imports: a value, a function, a once and a
// type of earlier inputs.
func TestReplVM_AHintOffersAnEarlierInputsName(t *testing.T) {
	replExpect(t, "abc = 3\nabd\nfn hello() -> Int\n  1\nend\nhelo()\nonce zed: Int = 4\nzef\n"+
		"struct Poynt\n  x: Int\nend\nPoint{x: 1}\n",
		"",
		"undefined variable 'abd'\n  help: did you mean 'abc'?",
		"undefined variable 'helo'\n  help: did you mean 'hello'?",
		"undefined variable 'zef'\n  help: did you mean 'zed'?",
		"unknown type \"Point\"\n  help: did you mean 'Poynt'?")
}
