package irbuild

import "testing"

// `impl calendar.DateParts for Q` in a file that declares its own
// `interface DateParts` implements std's interface, named through its
// module, and its impls are filed apart from the local interface's, so both
// impls for Q are called. The impl used to resolve its interface by the bare
// name, the local one, and was refused.
func TestIRStdIface_AQualifiedImplBesideALocalInterfaceOfItsName(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/calendar
    std/io
}

interface DateParts {
    fn year(value: self) -> Int
}

struct Q {
    y: Int
}

impl DateParts for Q {
    fn year(q: Q) -> Int {
        q.y + 1000
    }
}

impl calendar.DateParts for Q {
    fn year(q: Q) -> Int {
        q.y
    }

    fn month(q: Q) -> Int {
        q.y
    }

    fn day(q: Q) -> Int {
        q.y
    }
}

fn main() {
    io.print(Int.to_string(calendar.DateParts.year(#Q{y: 7})))
    io.print(Int.to_string(DateParts.year(#Q{y: 7})))
}
`, "7\n1007\n")
}
