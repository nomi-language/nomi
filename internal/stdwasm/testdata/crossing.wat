;; Imports of floats and of an i64 called through exports, an import
;; called after the memory grew, and a recursion that overflows the
;; stack, over a memory with a maximum that the module does not export.
;; crossing.wasm is this module as internal/stdwasm/fixtures_test.go
;; assembles it.
(module
  (import "env" "float" (func $float (param f64 f32) (result f64 f32)))
  (import "env" "shout" (func $shout (param i32 i32)))
  (import "env" "echo" (func $echo (param i64) (result i64)))
  (memory 1 16)

  (func (export "float") (param f64 f32) (result f64 f32)
    local.get 0
    local.get 1
    call $float)
  (func (export "grow_and_shout") (param i32 i32 i32)
    local.get 0
    memory.grow
    drop
    local.get 1
    local.get 2
    call $shout)
  (func $recurse (export "recurse")
    call $recurse)
  (func (export "echo") (param i64) (result i64)
    local.get 0
    call $echo))
