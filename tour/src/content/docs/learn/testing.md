---
title: "14. Testing your own code"
description: "First Steps, lesson 14: writing test blocks, reading a failed check, and choosing the values to test."
---

Every lesson so far has had checks under its task. They are **tests**, and
you can write your own. A `test` block has a name in quotes and holds
`assert` lines. Each `assert` states something that should be `True`:

```nomi-run
fn double(n: Int) -> Int
    n * 2
end

test "double"
    assert double(2) == 4
    assert double(5) == 10
end
```
<!-- expect
ok double
test result: ok. 1 passed, 0 failed
-->

When an `assert` is `False`, the test fails, and its report shows the
value your code made. Say `double` had a mistake, `n + 2` instead of
`n * 2`. The same test would report:

```text
FAIL main.nomi :: double
  line 7: assertion failed
    assert double(5) == 10
    values:
      double(5)
        = 7
test result: FAILED. 0 passed, 1 failed
```

It names the test and the line that failed, and shows that `double(5)` made
7, not 10.

Look at the first `assert`: `double(2) == 4` is `True` even with the
mistake, because 2 + 2 and 2 * 2 are both 4. A test that checks one value
can pass by luck. Check a few, and include the edges, where the answer
changes.

## Your turn

This time the function is right, and you write the tests. `is_teen` says
whether someone is a teenager: 13 to 19, both included.

Check runs your tests twice: first on this `is_teen`, where they should
all pass, and then on a copy with a mistake hidden in it, where at least
one should fail. The test below passes on both, so it can't tell them
apart. Add asserts until yours do.

```nomi-run lesson
fn is_teen(age: Int) -> Bool
    age >= 13 and age <= 19
end

// Write your tests below. Check runs them on this is_teen and on one with a mistake.
test "a teen and a child"
    assert is_teen(15)
    assert not is_teen(10)
end
```

```nomi-hint
A mistake like this usually hides at an edge, where the answer changes.
Test the ages on each side of both edges: 12, 13, 19 and 20.
```

```nomi-bug
fn is_teen(age: Int) -> Bool
    age >= 13 and age < 19
end
```

```nomi-solution
fn is_teen(age: Int) -> Bool
    age >= 13 and age <= 19
end

// Write your tests below. Check runs them on this is_teen and on one with a mistake.
test "a teen and a child"
    assert is_teen(15)
    assert not is_teen(10)
end

test "the edges"
    assert not is_teen(12)
    assert is_teen(13)
    assert is_teen(19)
    assert not is_teen(20)
end
```
