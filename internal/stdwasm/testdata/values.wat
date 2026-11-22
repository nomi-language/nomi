;; A function of each value type, the traps, memory growth, and an
;; `_initialize` that sets the global `ready` reads. values.wasm is this
;; module as internal/stdwasm/fixtures_test.go assembles it.
(module
  (memory (export "memory") 1)
  (global $ready (mut i32) (i32.const 0))

  (func (export "add_i32") (param i32 i32) (result i32)
    local.get 0
    local.get 1
    i32.add)
  (func (export "add_i64") (param i64 i64) (result i64)
    local.get 0
    local.get 1
    i64.add)
  (func (export "mul_f32") (param f32 f32) (result f32)
    local.get 0
    local.get 1
    f32.mul)
  (func (export "mul_f64") (param f64 f64) (result f64)
    local.get 0
    local.get 1
    f64.mul)

  ;; NaNs whose bits are not the canonical NaN's.
  (func (export "nan_payload_f64") (result f64)
    f64.const nan:0x4000000000001)
  (func (export "negative_nan_f64") (result f64)
    f64.const -nan)
  (func (export "negative_nan_f32") (result f32)
    f32.const -nan:0x400001)

  (func (export "unreachable")
    unreachable)
  (func (export "out_of_bounds") (result i32)
    i32.const -1
    i32.load)
  (func (export "divide") (param i32 i32) (result i32)
    local.get 0
    local.get 1
    i32.div_s)

  (func (export "grow") (param i32) (result i32)
    local.get 0
    memory.grow)
  (func (export "grow_or_trap") (param i32)
    local.get 0
    memory.grow
    i32.const -1
    i32.eq
    if
      unreachable
    end)

  (func (export "pair") (result i32 i64)
    i32.const 1
    i64.const 2)

  (func (export "_initialize")
    i32.const 42
    global.set $ready)
  (func (export "ready") (result i32)
    global.get $ready))
