;; `wasm.run`'s calling convention over a function that answers what an
;; earlier call would have left (its global, a word of its first page, and
;; a word of the page it grows) and then leaves 7 in each. A fresh instance
;; answers 12 zero bytes. residue.wasm is this module as
;; internal/stdwasm/fixtures_test.go assembles it.
(module
  (memory (export "memory") 1)
  (global $left (mut i32) (i32.const 0))

  (func (export "alloc") (param $size i32) (result i32)
    i32.const 0)

  (func (export "scribble") (param $at i32) (param $length i32) (result i64)
    ;; what was left, at 128, 132 and 136
    i32.const 128
    global.get $left
    i32.store
    i32.const 132
    i32.const 64
    i32.load
    i32.store
    i32.const 1
    memory.grow
    drop
    i32.const 136
    i32.const 65600
    i32.load
    i32.store
    ;; 7 left in each
    i32.const 7
    global.set $left
    i32.const 64
    i32.const 7
    i32.store
    i32.const 65600
    i32.const 7
    i32.store
    ;; the 12 bytes at 128
    i64.const 549755813900))
