// Package rt is Nomi's runtime library: the collections, exact Int
// arithmetic and its trap text, Float semantics, structural hashing,
// equality, scalar rendering, the frame, the task and supervisor runtime,
// and the test reporter, whose line diff is also the unified diff
// `nomi fmt -d` prints. The VM (internal/vm) runs over it, and built
// executables link it. Its internal
// organization may change freely to serve the VM.
//
// # It is a leaf
//
// rt imports only the Go standard library, one third-party module
// (github.com/rivo/uniseg, for grapheme clusters), and its own packages. It
// must never reach nomi/analysis, nomi/ast or nomi/parser: a VM runner that
// carries a program's bytecode has no front end in it, and rt is the library
// it links. rt shares a Go module with the compiler, so a new dependency of
// the module is not a dependency of rt unless rt imports it.
//
// This is enforced, not intended: see imports_test.go's
// TestRuntimeImportsOnlyItsAllowlist and internal/vm/runtime_deps_test.go's
// TestRuntimeArtifactLinksNoFrontEnd.
//
// # One implementation, not two agreeing ones
//
// rt owns the native-typed core (IntAdd and its siblings over Int values,
// DivByZeroError, FormatFloat over float64), and everything that needs one
// of those rules calls it. `9223372036854775807 + 1` is therefore
// 9223372036854775808 wherever it happens because the same lines run, not
// because a test says so. One thing in this package is genuinely a second
// encoding of a rule stated elsewhere, and it is called out where it is
// defined: FormatBool (std derives "True"/"False" from a Nomi `impl
// Display`).
//
// # Calling convention
//
// Functions that run Nomi code take `fr *Frame` first (see frame.go).
// Runtime faults are panics carrying *Error, recovered at the VM's boundary.
//
// A frame's Go context carries what the machine gives every frame: its
// locale data, phrase tables, streams, files and input. A supervised task's
// frame takes those values from the frame that spawned it and its
// cancellation from the supervisor (supervisor.go), so it outlives the
// request that spawned it and still reads the program's locale data rather
// than `cldrdata.Default`'s, which a runner leaves empty
// (TestSupervisorTaskReadsTheSpawnersContextValues, and vmhost's
// TestLocaleImage_ASupervisedTaskReadsTheProgramsLocales).
//
// # Int: exact at any size
//
// An `Int` holds a whole number of any size, and its arithmetic is exact. A
// value that fits in 64 bits stays as cheap as a plain 64-bit integer.
//
// A value that fits int64 is an `int64` everywhere: a [Value], a VM word
// register, a record's word slot. A value past int64's range is a
// [*BigInt], an immutable `math/big` integer (bigint.go). There is one
// canonical form: every operation and constructor returns an int64 when the
// value fits, so `big - big` that lands back in range is an int64 again,
// and a value that fits int64 is never a *BigInt. Two equal Ints therefore
// have the same Go type, and equality, hashing, ordering, Map keys and
// rendering compare like with like and need no normalization.
//
// A big value in a word slot occupies the word [BigWord] (2^63, the bits of
// `math.MinInt64`), and the value itself sits beside the word, where the
// collector can see it. In a record that is the record's big slots
// (record.go): references past the layout's own, one per word slot, which a
// record allocates only when it holds a big Int. A record that holds none
// keeps its plain layout, and code that writes an int64 word straight into
// a slot stays right, since a word of BigWord with no big slot beside it is
// its own int64, `math.MinInt64`. In the VM's registers it is the word
// bank's shadow (internal/vm package doc, "Big Ints in the word bank"),
// which also lists the representations not taken.
//
// A literal too long for int64 is carried through the AST and IR as a big
// constant, and the image encodes it as base-10 text.
//
// `Int.wrapping_add`, `wrapping_sub` and `wrapping_mul` ([IntWrapAdd] and
// its siblings, over [Wrap64]) return the low 64 bits of the exact result as
// a signed int64, so a hash a program writes stays a 64-bit number.
//
// # Hashing
//
// Every structural hash, `String.hash` and `Bytes.hash` among them, is keyed
// by a 128-bit key each process draws from crypto/rand when it starts, so
// nobody outside the process can choose keys that collide; NOMI_HASH_SEED
// fixes it, and a test binary fixes it for itself and its children
// (hashkey.go). Strings and Bytes hash by SipHash-1-3, composites fold
// through a keyed non-linear mix, and an Int and a Float keep their own
// bits (hash.go).
//
// # Deadlock
//
// A program stops with a report when no task of it can run again (spec §16
// *Deadlock*). The report may miss a deadlock and never names one that is
// not: a false report would kill a correct program.
//
// One registry (deadlock.go) holds a record per task, under one lock: each
// main line (`nomi run`'s main, an embedder's call, a test case: MainLine),
// each `concurrent` task and each supervised task. A record runs or is
// blocked, and the registry counts the running ones. A spawner records its
// task running before the task starts, and only a task's own goroutine
// blocks its record.
//
// Every wait that can block a task forever is a wait object with its own
// waiter list (wait.go): a channel's receivers and senders (channel.go), a
// select over several channels (channelselect.go), a task's end (latch),
// which `Task.await`, `Task.outcome` and `Task.await_all` wait for, a
// block's first failure, which `await_all` also waits for, a block's
// running tasks, which its end waits for, a `max_running` slot (semaphore)
// of a `spawn_all` batch or a supervisor, a `once` another task forces
// (once.go), and a supervisor's enrolled work, which `Supervisor.flush`
// waits for. A Go channel or WaitGroup would wake some goroutine without
// saying which; a wait object's waker knows exactly whom it wakes.
//
// A waiter puts its parker on the list of each object it waits on and
// records itself blocked while it holds the locks of all of them, so no
// waker finds it before the registry knows it waits. A waker takes it off
// the list and claims its parker under the object's lock, once, and records
// it running before the waker goes on, so before the waker can itself
// block. The invariant: the registry's count of running records is exact
// at the instant it reads zero. Every claim of a blocked record comes from
// a running task holding an object's lock, which records the waiter running
// before it can block itself; from the registry, delivering its report;
// or from something outside the tasks, which the check counts as able to
// wake the waiter for as long as it can claim it (below). So when the
// count reads zero and no blocked wait could end from outside, no claim is
// pending and none can come: every task waits for another, for good. The
// check runs at each moment the count can reach zero, when a task blocks
// or ends, and when something that could wake a waiter stops being able to.
//
// A wait could end from outside the tasks, and nothing is reported while
// one exists, when it has a timer of its own (`Arm.after`,
// `Receiver.receive_within`, a flush's bound); when its context has a
// deadline or has ended already (a `with` deadline, an embedder's deadline,
// a task whose cancel will wake it); when it is on a channel something
// outside the tasks feeds; or when it is a `Supervisor.flush`, whose
// supervisor's tasks may wait in ways no wait object records. Nothing is
// reported while a drain runs (it closes channels and cancels supervised
// tasks from goroutines of its own), nor while a task lives that no main
// line spawned (a program kept for a host's calls), nor once the registry
// found two goroutines waiting on one record, which it then distrusts for
// good. An embedder cancelling its call is no wake: it ends the call.
//
// What feeds a Nomi channel from outside the tasks, found in the code: a
// signal feed (SubscribeSignals), which sends from a goroutine of its own,
// and a `close_on_drain` hook (SenderCloseOnDrain), which a shutdown
// signal's drain runs. Each counts itself on its channel while it can send
// or close (taskRegistry.feed). Every other outside event reaches a program
// through a task in a call into Go: std's servers hand a request to a task
// waiting in `next`, its process waits, standard input and terminal reads,
// sockets, WebAssembly calls and `timer.sleep` are host calls their task
// makes, and a task in one is never blocked, since only a wait object
// blocks a record. A supervised task's restart delay and the input lock
// are waits of that kind too.
//
// The report goes to every blocked main line as the fault its wait raises:
// DeadlockText, then each of its tasks, what it waits for and where, the
// Site its machine gave the wait (internal/vm passes each blocking call's
// position). A machine running a case under a virtual clock also notes
// each call into Go on the calling task's record (EnterGo), which is where
// a case given up on waits (CaseStillRunning). In a virtual-clock bubble the
// report comes before the bubble finds every goroutine blocked, since the
// last task to block runs the check before it sleeps; a parker's channel is
// made by the goroutine that waits on it, so the bubble counts that wait as
// durably blocked.
//
// The registry is touched only when a task starts or ends, blocks or is
// woken: a send into a buffer with room, or a receive from a buffer with no
// sender waiting, takes the channel's lock alone.
//
// # Signals
//
// SIGINT and SIGTERM are shutdown's (shutdown.go): they drain the
// supervisors, run boot's cleanup and exit, and nothing else takes them.
// Every other signal the process catches goes through one registry
// (signals.go): a watch per signal number, registered with signal.Notify
// for that number alone, hands each arrival to the feeds that want it, and
// a feed sends a value on a channel its subscriber reads
// (SubscribeSignals). std::signals' receivers are feeds; another runtime
// feature a signal drives subscribes the same way, with the value computed
// at each arrival. A feed closes with the frame that made it and at the
// drain, and a signal no feed wants gets its default action back.
package rt
