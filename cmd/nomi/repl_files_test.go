package main

import "testing"

// Inputs as files: what an input reads of earlier inputs' files.

// A generic function an earlier input declared is instantiated at the types
// a later input calls it with, which no earlier lowering made.
func TestReplVM_AnEarlierGenericIsInstantiatedLater(t *testing.T) {
	replExpect(t, "fn pair<T>(x: T) -> #(T, T) {\n  #(x, x)\n}\npair(1)\npair(\"s\")\n"+
		"struct Box<T> {\n  v: T\n}\nb = Box{v: [1, 2]}\nb\nBox{v: pair(True)}\n",
		"#(1, 1)\n#(\"s\", \"s\")\nBox{v: [1, 2]}\nBox{v: #(True, True)}\n")
}

// An impl redefined in a later input replaces the earlier one for the
// inputs after it, and an interface declared in one input is implemented
// and called in others.
func TestReplVM_ARedefinedImplReplacesTheEarlierOne(t *testing.T) {
	replExpect(t, "struct P {\n  x: Int\n}\n"+
		"impl Display for P {\n  fn to_string(p: P) -> String {\n    \"one ${p.x}\"\n  }\n}\n\"${P{x: 1}}\"\n"+
		"impl Display for P {\n  fn to_string(p: P) -> String {\n    \"two ${p.x}\"\n  }\n}\n\"${P{x: 2}}\"\n"+
		"impl P {\n  pub fn get(p: P) -> Int {\n    p.x\n  }\n}\nP.get(P{x: 3})\n"+
		"impl P {\n  pub fn get(p: P) -> Int {\n    p.x * 10\n  }\n}\nP.get(P{x: 3})\n"+
		"interface Shape {\n  fn area(s: self) -> Int\n}\n"+
		"impl Shape for P {\n  fn area(p: P) -> Int {\n    p.x * p.x\n  }\n}\n"+
		"fn total(s: Shape) -> Int {\n  Shape.area(s)\n}\ntotal(P{x: 4})\n",
		"\"one 1\"\n\"two 2\"\n3\n30\n16\n")
}

// An input that fails after replacing an impl puts the earlier impl back.
func TestReplVM_AFailedRedefinitionKeepsTheEarlierImpl(t *testing.T) {
	replExpect(t, "struct P {\n  x: Int\n}\n"+
		"impl Display for P {\n  fn to_string(p: P) -> String {\n    \"one ${p.x}\"\n  }\n}\n"+
		"impl Display for P {\n  fn to_string(p: P) -> String {\n    missing_name\n  }\n}\n\"${P{x: 1}}\"\n",
		"\"one 1\"\n",
		"undefined variable 'missing_name'")
}

// An enum and its variants carry between inputs.
func TestReplVM_AnEnumCarriesBetweenInputs(t *testing.T) {
	replExpect(t, "enum Color {\n  Red\n  Green\n}\nc = Color.Green\nc\nc == Color.Red\n"+
		"fn name(c: Color) -> String {\n  case c {\n    Color.Red -> \"red\"\n    Color.Green -> \"green\"\n  }\n}\nname(c)\n",
		"Green\nFalse\n\"green\"\n")
}

// A `fn boot` runs with every input after it, so a later input, and a
// function a later input declares, reads its application fields.
func TestReplVM_LaterInputsRunUnderAnEarlierBoot(t *testing.T) {
	replExpect(t, "struct App {\n  n: Int\n}\nfn boot() -> Result<App, String> {\n  Ok(App{n: 3})\n}\n"+
		"App.n\nfn twice() -> Int {\n  App.n * 2\n}\ntwice()\nx = 5\nx + App.n\n",
		"3\n6\n8\n")
}

// A derive in a later input implements the interface for an earlier
// input's type, for the inputs after it.
func TestReplVM_ALaterInputDerivesForAnEarlierType(t *testing.T) {
	replExpect(t, "struct Pt {\n  x: Int\n}\nderive Comparable for Pt\n"+
		"[Pt{x: 2}, Pt{x: 1}] |> Iter.sort() |> Iter.to_list()\n"+
		"derive Hashable for Pt\nm = %{Pt{x: 1} => \"one\"}\nMap.get(m, Pt{x: 1})\n",
		"[Pt{x: 1}, Pt{x: 2}]\nSome(\"one\")\n")
}

// A redefined impl replaces the earlier one for code earlier inputs
// compiled too: a function that called the old impl's function, directly
// or through string interpolation, calls the new one.
func TestReplVM_ARedefinedImplReachesEarlierFunctions(t *testing.T) {
	replExpect(t, "struct P {\n  x: Int\n}\n"+
		"impl Display for P {\n  fn to_string(p: P) -> String {\n    \"one ${p.x}\"\n  }\n}\n"+
		"fn show(p: P) -> String {\n  \"${p}\"\n}\nshow(P{x: 1})\n"+
		"impl Display for P {\n  fn to_string(p: P) -> String {\n    \"two ${p.x}\"\n  }\n}\nshow(P{x: 1})\n"+
		"impl P {\n  pub fn get(p: P) -> Int {\n    p.x\n  }\n}\n"+
		"fn use_get(p: P) -> Int {\n  P.get(p)\n}\nuse_get(P{x: 3})\n"+
		"impl P {\n  pub fn get(p: P) -> Int {\n    p.x * 10\n  }\n}\nuse_get(P{x: 3})\n",
		"\"one 1\"\n\"two 1\"\n3\n30\n")
}

// A derive in a later input for an earlier input's type replaces the impl
// the type had, for every derivable interface: the Debug every type has
// with no derive, a hand-written impl, and an earlier derive. Code an
// earlier input compiled calls the new impl too.
func TestReplVM_ALaterDeriveReplacesTheTypesImpl(t *testing.T) {
	replExpect(t, "import std::json.{Json, ToJson, FromJson}\n"+
		"struct R {\n  x: Int\n}\nfn show(r: R) -> String {\n  Debug.inspect(r)\n}\n"+
		"derive Debug for R\nR{x: 5}\n[R{x: 6}]\n"+
		"impl Debug for R {\n  fn inspect(r: R) -> String {\n    \"custom ${r.x}\"\n  }\n}\nshow(R{x: 7})\n"+
		"derive Debug for R\nshow(R{x: 8})\n"+
		"impl Display for R {\n  fn to_string(r: R) -> String {\n    \"hand ${r.x}\"\n  }\n}\n\"${R{x: 1}}\"\n"+
		"derive Display for R\n\"${R{x: 2}}\"\n"+
		"impl Equatable for R {\n  fn equal(_: R, _: R) -> Bool {\n    True\n  }\n}\n"+
		"fn same(a: R, b: R) -> Bool {\n  a == b\n}\nsame(R{x: 1}, R{x: 2})\n"+
		"derive Equatable for R\nsame(R{x: 1}, R{x: 2})\n"+
		"derive Comparable for R\nR{x: 1} < R{x: 2}\nderive Comparable for R\nR{x: 3} < R{x: 2}\n"+
		"derive Hashable for R\nMap.get(%{R{x: 1} => 1}, R{x: 1})\nderive Hashable for R\nMap.get(%{R{x: 2} => 2}, R{x: 2})\n"+
		"derive ToJson for R\nJson.encode_as(R{x: 1})\nderive ToJson for R\nJson.encode_as(R{x: 2})\n"+
		"derive FromJson for R\nJson.decode_as<R>(\"{\\\"x\\\": 3}\")\n"+
		"enum Coin {\n  Penny\n  Nickel\n}\nderive Variants for Coin\nCoin.variants()\n",
		"R{x: 5}\n[R{x: 6}]\n\"custom 7\"\n\"R{x: 8}\"\n\"hand 1\"\n\"R{x: 2}\"\nTrue\nFalse\n"+
			"True\nFalse\nSome(1)\nSome(2)\n\"{\\\"x\\\":1}\"\n\"{\\\"x\\\":2}\"\nOk(R{x: 3})\n[Penny, Nickel]\n")
}

// An undefined name's hint offers every name the session has in scope, not
// only the ones the input's file imports: a value, a function, a once and a
// type of earlier inputs.
func TestReplVM_AHintOffersAnEarlierInputsName(t *testing.T) {
	replExpect(t, "abc = 3\nabd\nfn hello() -> Int {\n  1\n}\nhelo()\nonce zed: Int = 4\nzef\n"+
		"struct Poynt {\n  x: Int\n}\nPoint{x: 1}\n",
		"",
		"undefined variable 'abd'\n  help: did you mean 'abc'?",
		"undefined variable 'helo'\n  help: did you mean 'hello'?",
		"undefined variable 'zef'\n  help: did you mean 'zed'?",
		"unknown type \"Point\"\n  help: did you mean 'Poynt'?")
}
