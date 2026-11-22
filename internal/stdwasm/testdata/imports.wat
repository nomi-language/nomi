;; Two functions that call the imports they are named for: one over the
;; caller's memory and one over a value. imports.wasm is this module as
;; internal/stdwasm/fixtures_test.go assembles it.
(module
  (import "env" "shout" (func $shout (param i32 i32)))
  (import "env" "twice" (func $twice (param i64) (result i64)))
  (memory (export "memory") 1)

  (func (export "shout") (param i32 i32)
    local.get 0
    local.get 1
    call $shout)
  (func (export "twice") (param i64) (result i64)
    local.get 0
    call $twice))
