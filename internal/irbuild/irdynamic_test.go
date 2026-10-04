package irbuild

import "testing"

// std/dynamic on the VM: a Dynamic is an rt.Dynamic, the
// thirteen host functions are internal/stdlibbindings RtFuncs rows with
// generated adapters, and `List<Dynamic>` and `Map<String, Dynamic>` ride in
// a Result. The program navigates by field, dotted path and index, extracts
// each scalar kind, a list and a dict, reads both decode failures, and
// renders the whole value.
func TestIRDynamic_NavigationAndExtractionRunOnTheVM(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
import std/json.Json
import std/dynamic.{Dynamic, DecodeError}

fn show_error(e: DecodeError): String {
  "expected " + e.expected + ", got " + e.got
}

fn string_at(d: Dynamic, name: String): String {
  case Dynamic.field(d, name) {
    Err(e) -> show_error(e)
    Ok(v) ->
      case Dynamic.as_string(v) {
        Ok(s) -> s
        Err(e) -> show_error(e)
      }
  }
}

fn int_at(d: Dynamic, dotted: String): String {
  case Dynamic.path(d, dotted) {
    Err(e) -> show_error(e)
    Ok(v) ->
      case Dynamic.as_int(v) {
        Ok(n) -> Int.to_string(n)
        Err(e) -> show_error(e)
      }
  }
}

fn tag_at(d: Dynamic, i: Int): String {
  case Dynamic.field(d, "tags") {
    Err(e) -> show_error(e)
    Ok(tags) ->
      case Dynamic.index(tags, i) {
        Err(e) -> show_error(e)
        Ok(v) ->
          case Dynamic.as_string(v) {
            Ok(s) -> s
            Err(e) -> show_error(e)
          }
      }
  }
}

fn kinds(d: Dynamic): String {
  pi = case Dynamic.path(d, "pi") {
    Ok(v) ->
      case Dynamic.as_float(v) {
        Ok(x) -> Float.to_string(x)
        Err(e) -> show_error(e)
      }
    Err(e) -> show_error(e)
  }
  on = case Dynamic.field(d, "on") {
    Ok(v) ->
      case Dynamic.as_bool(v) {
        Ok(b) -> Bool.to_string(b)
        Err(e) -> show_error(e)
      }
    Err(e) -> show_error(e)
  }
  tags = case Dynamic.field(d, "tags") {
    Ok(v) ->
      case Dynamic.as_list(v) {
        Ok(xs) -> Int.to_string(Iter.count(xs))
        Err(e) -> show_error(e)
      }
    Err(e) -> show_error(e)
  }
  keys = case Dynamic.as_dict(d) {
    Ok(m) -> Int.to_string(Map.size(m))
    Err(e) -> show_error(e)
  }
  nothing = case Dynamic.field(d, "nothing") {
    Ok(v) -> Bool.to_string(Dynamic.null?(v))
    Err(e) -> show_error(e)
  }
  pi + " " + on + " " + tags + " " + keys + " " + nothing
}

fn run(d: Dynamic) {
  io.print(kinds(d))
  io.print(string_at(d, "name"))
  io.print(string_at(d, "age"))
  io.print(string_at(d, "missing"))
  io.print(int_at(d, "inner.n"))
  io.print(tag_at(d, 1))
  io.print(tag_at(d, 5))
  io.print(Dynamic.has?(d, "age"))
  io.print(Dynamic.has?(d, "missing"))
  io.print(Dynamic.null?(d))
  io.print(Dynamic.inspect(d))
}

fn main() {
  source = "{\"name\": \"ada\", \"age\": 36, \"tags\": [\"x\", \"y\"], \"inner\": {\"n\": 7}, \"pi\": 3.5, \"on\": true, \"nothing\": null}"
  case Json.decode(source) {
    Err(_) -> io.print("bad json")
    Ok(j) -> run(Json.to_dynamic(j))
  }
}
`, `3.5 True 2 7 True
ada
expected String, got Int
expected object with field "missing", got object without field "missing"
7
y
expected list with at least 6 elements, got list with 2 elements
True
False
False
{"age": 36, "inner": {"n": 7}, "name": "ada", "nothing": null, "on": true, "pi": 3.5, "tags": ["x", "y"]}
`)
}
