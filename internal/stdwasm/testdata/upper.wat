;; `wasm.run`'s calling convention: a bump allocator, and a function that
;; uppercases the ASCII letters of its input in place and returns where it
;; is, its pointer in the high 32 bits and its length in the low 32.
;; upper.wasm is this module as internal/stdwasm/fixtures_test.go
;; assembles it.
(module
  (memory (export "memory") 1)
  (global $next (mut i32) (i32.const 1024))

  (func (export "alloc") (param $size i32) (result i32)
    (local $at i32)
    global.get $next
    local.set $at
    global.get $next
    local.get $size
    i32.add
    global.set $next
    local.get $at)

  (func (export "upper") (param $at i32) (param $length i32) (result i64)
    (local $i i32) (local $c i32)
    block $done
      loop $each
        local.get $i
        local.get $length
        i32.ge_u
        br_if $done
        ;; the address, then the byte there less 32 when it is a-z
        local.get $at
        local.get $i
        i32.add
        local.get $at
        local.get $i
        i32.add
        i32.load8_u
        local.tee $c
        local.get $c
        i32.const 97
        i32.sub
        i32.const 26
        i32.lt_u
        i32.const 32
        i32.mul
        i32.sub
        i32.store8
        local.get $i
        i32.const 1
        i32.add
        local.set $i
        br $each
      end
    end
    local.get $at
    i64.extend_i32_u
    i64.const 32
    i64.shl
    local.get $length
    i64.extend_i32_u
    i64.or))
