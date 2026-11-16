---
title: "17. A small program"
description: "First Steps, lesson 17: putting structs, decisions, pipes and printing together into one program."
---

A real program is the pieces from these lessons, each doing one job. Here
is one that prints a class's results. A struct holds each student, `grade`
decides a letter, and `main` puts them together and prints a line each:

```nomi-run
import std::io

struct Student
    name:  String
    score: Int
end

fn grade(score: Int) -> String
    case
        score >= 90 -> "A"
        score >= 70 -> "B"
        score >= 50 -> "C"
        _           -> "F"
    end
end

fn main()
    students = [
        Student{name: "Ada", score: 95},
        Student{name: "Bo", score: 72},
        Student{name: "Cy", score: 40},
    ]

    students
    |> Iter.map(fn s -> "${s.name}: ${s.score} (${grade(s.score)})")
    |> Iter.each(io.print)
end
```
<!-- expect
Ada: 95 (A)
Bo: 72 (B)
Cy: 40 (F)
-->

Each function is small enough to read in one go and to test on its own:
a test of `grade` needs no students and prints nothing. When a program
grows, it grows by more functions like these.

## Your turn

`report` gives back the program's lines as a list, so tests can check them.
Under the students' lines it should add a summary: how many passed (a pass
is 50 or more) out of how many students, as in `"2 of 3 passed"`. Right
now it gives back the students' lines alone. `+` joins two lists, as it
joins two pieces of text: `["a"] + ["b"]` is `["a", "b"]`.

```nomi-run lesson
struct Student
    name:  String
    score: Int
end

fn grade(score: Int) -> String
    case
        score >= 90 -> "A"
        score >= 70 -> "B"
        score >= 50 -> "C"
        _           -> "F"
    end
end

fn report(students: List<Student>) -> List<String>
    students
    |> Iter.map(fn s -> "${s.name}: ${s.score} (${grade(s.score)})")
    |> Iter.to_list()
end

// The checks below run when you press Check. You don't need to change them.
test "a class of three"
    students = [
        Student{name: "Ada", score: 95},
        Student{name: "Bo", score: 72},
        Student{name: "Cy", score: 40},
    ]

    assert report(students) == ["Ada: 95 (A)", "Bo: 72 (B)", "Cy: 40 (F)", "2 of 3 passed"]
end

test "an empty class"
    assert report([]) == ["0 of 0 passed"]
end
```

```nomi-hint
Name the students' lines first, lines = .... Count the students who passed
with Iter.filter and Iter.count, and give back lines + ["... passed"].
```

```nomi-solution
struct Student
    name:  String
    score: Int
end

fn grade(score: Int) -> String
    case
        score >= 90 -> "A"
        score >= 70 -> "B"
        score >= 50 -> "C"
        _           -> "F"
    end
end

fn report(students: List<Student>) -> List<String>
    lines =
        students
        |> Iter.map(fn s -> "${s.name}: ${s.score} (${grade(s.score)})")
        |> Iter.to_list()

    passed = students |> Iter.filter(fn s -> s.score >= 50) |> Iter.count()
    lines + ["${passed} of ${Iter.count(students)} passed"]
end

// The checks below run when you press Check. You don't need to change them.
test "a class of three"
    students = [
        Student{name: "Ada", score: 95},
        Student{name: "Bo", score: 72},
        Student{name: "Cy", score: 40},
    ]

    assert report(students) == ["Ada: 95 (A)", "Bo: 72 (B)", "Cy: 40 (F)", "2 of 3 passed"]
end

test "an empty class"
    assert report([]) == ["0 of 0 passed"]
end
```

## Where to go next

That's the end of First Steps. You can now write and test small programs
of your own: try changing this one, to give an A* for 95 and up, or to
list only the students who passed.

The [Language Tour](/introduction/) covers all of Nomi, at a
quicker pace, for readers who know some programming, which now includes
you. The
[Cheat Sheet](/cheat-sheet/) lists the rules on one page, and the
[Standard Library](/reference/) has every function, `Iter` and `String`
among them.
