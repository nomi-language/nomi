package vm_test

// THE VM AGAINST THE REPORT-SHAPED HALF OF THE COMMITTED EXPECTATIONS.
//
// `expectation_test.go` compares whole-PROGRAM transcripts and leaves the
// records whose transcript is a test report to this file. The unit here is a
// FILE'S TEST REPORT — what `nomi test <file>` prints — which is a different
// unit from that file's and therefore a different denominator, kept in a
// separate table for that reason rather than for convenience.
//
// A report needs both halves: the producer retains each test body as a test
// case (`ir.Module.Tests()`, apart from the declarations in `Funcs()`), and
// this package's runner drives them.
// `TestVMReport_TestCasesAreRetainedApartFromDeclarations` checks the first
// half.
//
// # WHAT A COMPARABLE RECORD REQUIRES, AND WHY EACH FILTER IS A PROPERTY
//
//	N > 0                the transcript is the report. That is the FILTER
//	                     IN here, and the exclusion in the other file.
//	every case retained  `len(mod.Tests())` must equal the recorded case
//	                     count. A file whose producer declined one case
//	                     would produce a transcript missing that case's
//	                     line, and comparing a partial report against a
//	                     whole one reports a RETENTION BOUNDARY as a wrong
//	                     answer.
//	every case runnable  a body that reaches an instruction this machine
//	                     has no arm for is NOT a failing case. `Machine.
//	                     RunTests` answers a reason instead of a report for
//	                     exactly that, because a machine that reported its
//	                     own limits as failure text would be comparing
//	                     itself.
//
// # WHY THE FAILURE POPULATION IS THE POINT
//
// It is the only one of the four with any power over a WRONG FAILURE MESSAGE:
// its records carry their transcripts VERBATIM because a hash says "this
// moved" and cannot show what a wrong diagnostic looks like. A passing corpus
// only ever compares runs in which everything passed, so it cannot catch a
// report that prints the wrong failure text.
//
// So the two controls below are about the MESSAGE and not about a value:
// `TestVMReport_APlantedWrongFailureMessageIsCaught` deletes the `values:`
// rows from the report, and
// `TestVMReport_APlantedWrongSourceFragmentIsCaught` mutates inside
// `rt`'s own report layout through the `rt.Highlight` seam and requires the
// FAILING records to be reported and the PASSING ones not to be.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// reportPins is one population's measured shape for THIS unit.
//
// The record count and the report-shaped count are read from `vmPopulations`
// in expectation_test.go, which owns them, so this table owns only what is
// its own: how many of the report-shaped records the VM can actually compare.
// `comparable` moving UP is the producer widening, while moving DOWN is a
// regression `CompareSubset` forgives by construction — it reports nothing
// about a subset that got smaller.
type reportPins struct {
	comparable int
	// why is what the population's zero or shortfall is, in one phrase, so a
	// reader of the table does not have to run it to learn the cause.
	why string
}

func (p reportPins) records(population string) int {
	return vmPopulations[population].records()
}

func (p reportPins) reportShaped(population string) int {
	return vmPopulations[population].reportShaped
}

// vmReportPopulations is the measured state of all four artifacts under this
// unit.
var vmReportPopulations = map[string]reportPins{
	"corpus": {comparable: 110,
		why: "every report-shaped corpus file whose cases all retain; the rest decline at least one case"},
	"tour": {comparable: 16,
		why: "all 16 report-shaped tour records"},
	"stdlib": {comparable: 0,
		why: "a stdlib record's id is a module, not a path this unit lowers; " +
			"TestExpectation_StdlibOnTheVM compares every stdlib case the VM runs"},
	"failure": {comparable: 49,
		why: "every failure fixture listed in vmReportComparable; " +
			"other fixtures reach Decimal, collections or unsupported subjects"},
}

// THE THREE NAMED LISTS THE TWO MESSAGE PLANTS ARE JUDGED AGAINST, and they
// are not the same list because the two plants reach different parts of a
// report. Each comparable record is in exactly the lists whose PROPERTY its
// recorded transcript has, read off the committed artifact:
//
//	vmReportWithValueRows        the report prints `values:` rows
//	vmReportWithSourceFragment   the report QUOTES the assertion's source
//	vmReportComparable           every comparable record, for the complement
//
// NAMED RATHER THAN DERIVED, for `vmDbgRecords`' reason: a list computed by
// "which transcripts contain `values:`" would be computed from the output
// under test. And the COMPLEMENT is what makes each plant a plant rather than
// a global mutation — a mutation that changed every record would prove the
// comparison sees SOMETHING, not that it sees the failure report.
//
// The three exclusions are each a property of the record and worth reading:
//
//   - `tests_mixed` and `tests_refute` print NO rows even though they fail,
//     because every operand of `1 == 2` and `2 > 1` reads exactly like its
//     value and `rt.RecordOperand`'s redundancy rule drops it. So they are in
//     the source-fragment list and not in the rows list, which is itself a
//     check on that rule: a producer that stopped requesting suppression
//     would put rows into those two reports and this control would fail.
//   - `tests_trap` and `virtual_clock_trap` print neither: a Nomi fault's
//     report is one `line N:` header, with no assertion behind it to quote and
//     no operands to show.
//   - the passing records print neither, because a passing case prints one
//     `ok` line and a passing assertion renders nothing at all.
//   - `pipe_assert_report.nomi` is in BOTH lists: its retained case is the
//     one that writes the call DIRECTLY, which prints rows, so the record
//     responds to both plants. The piped cases beside it print none — that
//     is the divergence the fixture pins — and they are not retained, so this
//     list's membership says nothing about them either way.
var vmReportWithValueRows = []string{
	"dbg_surfaces.nomi",
	"anon_struct.nomi",
	"inspect_through_list.nomi",
	"untyped_list_report.nomi",
	"tests_attached.nomi",
	"context_values_report.nomi",
	"pipe_stage_keyword_report.nomi",
	"tests_case_subject.nomi",
	"assert_boundary.nomi",
	"assertable_subject_report.nomi",
	"anon_vs_nominal.nomi",
	"generic_struct.nomi",
	"std_enum_modes.nomi",
	"tests_operands.nomi",
	"untyped_equality_report.nomi",
	"siblings/report_test.nomi",
	"sibimpl/report_test.nomi",
	"tests_call_boundary.nomi",
	"tests_lambda_boundary.nomi",
	"pipe_assert_report.nomi",
	"tests_fail.nomi",
	"tests_group_failure.nomi",
	"tests_short_circuit.nomi",
	"unit_value.nomi",
	"decimal.nomi",
	"tests_inspect_vs_display.nomi",
	"debug_embeds.nomi",
	"debug_erased.nomi",
	"erased_operand.nomi",
	"erased_operand_row.nomi",
	"func_operand_row.nomi",
	"impl_call_operand_row.nomi",
	"debug_display_surfaces.nomi",
	"iter_operand_report.nomi",
}

var vmReportWithSourceFragment = []string{
	"untyped_list_report.nomi",
	"dbg_surfaces.nomi",
	"anon_struct.nomi",
	"inspect_through_list.nomi",
	"tests_attached.nomi",
	"try_attached.nomi",
	"try_check_in_test.nomi",
	"try_in_test.nomi",
	"context_values_report.nomi",
	"pipe_stage_keyword_report.nomi",
	"tests_case_subject.nomi",
	"assert_boundary.nomi",
	"assert_shape.nomi",
	"assertable_subject_report.nomi",
	"check_shape_report.nomi",
	"anon_vs_nominal.nomi",
	"generic_struct.nomi",
	"std_enum_modes.nomi",
	"tests_operands.nomi",
	"untyped_equality_report.nomi",
	"siblings/report_test.nomi",
	"sibimpl/report_test.nomi",
	"tests_call_boundary.nomi",
	"tests_lambda_boundary.nomi",
	"pattern_assert.nomi",
	"pipe_assert_report.nomi",
	"tests_defined_as.nomi",
	"tests_group_failure.nomi",
	"tests_fail.nomi",
	"tests_mixed.nomi",
	"tests_refute.nomi",
	"tests_short_circuit.nomi",
	"unit_value.nomi",
	"decimal.nomi",
	"tests_inspect_vs_display.nomi",
	"debug_embeds.nomi",
	"debug_erased.nomi",
	"erased_operand.nomi",
	"erased_operand_row.nomi",
	"func_operand_row.nomi",
	"impl_call_operand_row.nomi",
	"debug_display_surfaces.nomi",
	"iter_operand_report.nomi",
	"pipe_stages.nomi",
	"ordering.nomi",
}

// vmReportComparable is every record this unit compares, by population.
var vmReportComparable = map[string][]string{
	"corpus": {
		// A struct that holds itself through a List, Maybe, Map or Iter.
		"07-structs-and-enums/recursive_structs_test.nomi",
		// Inherent impl blocks on user generic types.
		"10-generics-and-type-wrappers/generic_inherent_impl_test.nomi",
		// `try` in a setup body and in a nested fn, Debug of a Set of declared
		// members, and sort_by over container and std keys.
		"02-testing/setup_try_test.nomi",
		"09-functions-and-control-flow/nested_fn_try_test.nomi",
		"12-derives-and-standard-interfaces/set_debug_test.nomi",
		"13-iterators-and-pipes/sort_keys_test.nomi",
		// Debug of a lazy Iter is the `<iter>` placeholder.
		"13-iterators-and-pipes/iter_inspect_test.nomi",
		// A declared `Iter<T>` is a value type.
		"13-iterators-and-pipes/iter_values_test.nomi",
		// std's Toml handler over `Fragment<Display>`.
		"17-typed-literals/toml_literal_test.nomi",
		// User generic enum variants and impls on
		// generic receivers instantiated from the checker's solved types,
		// a struct-shaped variant carrying a struct, a binding declared at
		// an interface type, a bound-solved generic argument, and a generic
		// enum's struct variant across a module.
		"10-generics-and-type-wrappers/bound_type_params_test.nomi",
		"10-generics-and-type-wrappers/generics_test.nomi",
		"11-interfaces-and-impls/interface_field_update_test.nomi",
		"11-interfaces-and-impls/type_argument_inference/type_argument_inference_test.nomi",
		"14-modules-and-packaging/module_qualified_literals/module_qualified_literals_test.nomi",
		// User operators and qualified operator impls,
		// Iter.chunks over `[]` and a recursive user Iter source.
		"12-derives-and-standard-interfaces/add_test.nomi",
		"13-iterators-and-pipes/iter_test.nomi",
		"15-app-and-defer/import_vs_app/import_vs_app_test.nomi",
		// `with` statements: consecutive lines, a `.Variant` value, a
		// closure called later and a branch.
		"15-app-and-defer/with_overrides/with_overrides_test.nomi",
		// `Maybe.hash(None)`'s hole-typed call lowers to an
		// unreachable fault.
		"12-derives-and-standard-interfaces/maybe_result_equatable_test.nomi",
		// `Bool.False(x)` binds a marker of std/bool's False.
		"12-derives-and-standard-interfaces/display_debug_test.nomi",
		// Context.with_value and Context.value over Type witnesses.
		"15-app-and-defer/context_values_test.nomi",
		// Struct and map patterns in `assert pattern =
		// value`, payloads over existentials, functions and composite
		// distincts, and a bare aliased std method in a test body.
		"02-testing/assertion_pattern_destructuring_test.nomi",
		"08-pattern-matching/variant_payloads_test.nomi",
		"14-modules-and-packaging/imports_and_modules_test.nomi",
		"15-app-and-defer/context_module_values/context_module_values_test.nomi",
		// Maybe, Result and Assertable subjects, assertions inside an
		// ordinary fn and a check over a pipe.
		"02-testing/assert_refute_test.nomi",
		"02-testing/assertable_test.nomi",
		"02-testing/assertion_pipe_test.nomi",
		"02-testing/assertion_rendering_test.nomi",
		"02-testing/assertion_unwrap_test.nomi",
		"04-scalars-and-text/ints_test.nomi",
		// Generic std bodies instantiated per program.
		"03-tooling-and-diagnostics/formatting_test.nomi",
		"04-scalars-and-text/random_test.nomi",
		"18-ffi-and-dynamic/dynamic_decoder_test.nomi",
		"18-ffi-and-dynamic/json_container_decode_test.nomi",
		// Enum Equatable impls, opaque types, derived JSON and
		// the struct call form over a record value.
		"07-structs-and-enums/enum_equatable_impl/enum_equatable_impl_test.nomi",
		"10-generics-and-type-wrappers/opaque_types/opaque_types_test.nomi",
		"18-ffi-and-dynamic/json_derive_test.nomi",
		"07-structs-and-enums/struct_call_form_record_test.nomi",
		// Test-body statement shapes: tuple destructures,
		// rebinding, block-valued bindings, nested `fn`s, single-branch
		// `case`s, and List.head over sorted std structs.
		"01-foundations/literals_test.nomi",
		"01-foundations/shadowing_test.nomi",
		"05-calendar-and-time/naive_datetime_test.nomi",
		"05-calendar-and-time/times_test.nomi",
		"06-collections/tuples_test.nomi",
		"11-interfaces-and-impls/same_named_stdlib_type/same_named_stdlib_type_test.nomi",
		"01-foundations/raw_strings_test.nomi",
		"01-foundations/triple_quoted_strings_test.nomi",
		// `${` escapes and the text `$`, `#` and `#{` in strings.
		"01-foundations/interpolation_escapes_test.nomi",
		"02-testing/setup_semantics_test.nomi",
		// Qualified construction of an embedded struct (`Shape.Circle{...}`)
		// retains as the struct widened into its variant.
		"07-structs-and-enums/embeds_positional_match_test.nomi",
		"12-derives-and-standard-interfaces/cross_file_debug/cross_file_debug_test.nomi",
		// A sibling file's `fn` retains once its signature is translated into
		// this unit as the walk translates it, so `app.load`'s Config result is
		// this package's mirror and its fields read.
		"15-app-and-defer/config_pattern/config_pattern_test.nomi",
		// Every grouped case retains once a walked case takes a named
		// function's comparison, prelude, task-call and annotated-binding
		// routes, `Iter.any?` retains, and drain_alerts' empty `Iter.loop`
		// seed takes the checker's parameter type.
		// Nested variant patterns (`Some(Request.Get{reply})`), Channel
		// fields in a struct and a struct-shaped variant, and a called
		// supervisor piped into `Supervisor.spawn` retain every function.
		"16-concurrency/message_loop/message_loop_test.nomi",
		"16-concurrency/supervisors/supervisors_test.nomi",
		// Lambda pipe stages read back once a test body admits lambdas.
		"13-iterators-and-pipes/pipe_lambda_test.nomi",
		// An ungrouped body the read-back attempt declines is retained
		// walk-only, with a walked body's routes: a qualified call in an
		// assertion subject records its rows (instant_test's
		// `Instant.to_seconds`, re_export_facade_test's `leaf.double`),
		// prelude constructors (try_error_conversion_test's `Ok`) and a
		// turbofish call (turbofish_test).
		"05-calendar-and-time/instant_test.nomi",
		"08-pattern-matching/try_error_conversion_test.nomi",
		// std's Duration.inspect retains once its bare `to_string(d)` is the
		// rt.DurationToString host, so every Duration case's Debug links.
		"05-calendar-and-time/durations_test.nomi",
		"10-generics-and-type-wrappers/turbofish_test.nomi",
		"14-modules-and-packaging/re_export_facade/re_export_facade_test.nomi",
		// A `try` whose boundary is the test body retains in a walk-only body,
		// and calendar's extern hosts cross by their binding name.
		"05-calendar-and-time/datetime_zone_changes_test.nomi",
		"05-calendar-and-time/dst_fall_back_test.nomi",
		"05-calendar-and-time/dst_spring_forward_test.nomi",
		"05-calendar-and-time/offset_datetime_test.nomi",
		// `assert f(x) == None`: the bare variant takes the other operand's type.
		"08-pattern-matching/try_test.nomi",
		// Calendar construction and arithmetic retain in std.
		"05-calendar-and-time/dates_test.nomi",
		"05-calendar-and-time/datetime_test.nomi",
		// A 3-segment qualifier (`Json.DecodeError.to_string(e)`) resolves as
		// its namespaced type's owner.
		"18-ffi-and-dynamic/json_decode_error_text_test.nomi",
		// A bare call to a selectively imported sibling `fn` links to the
		// declaring file's body.
		"14-modules-and-packaging/cross_file_modules/cross_file_modules_test.nomi",
		"14-modules-and-packaging/explicit_item_imports/explicit_item_imports_test.nomi",
		// A call to an erased generic function runs a monomorphic instance.
		"10-generics-and-type-wrappers/bounds_test.nomi",
		// `==`/`!=` dispatch to an Equatable impl or compare structurally
		// (irequality.go), `List.equal?`/`Maybe.equal?` compare, and struct
		// literals with container fields, generic arguments and std or
		// sibling defaults build.
		"04-scalars-and-text/codepoints_test.nomi",
		"04-scalars-and-text/floats_test.nomi",
		"04-scalars-and-text/ranges_test.nomi",
		"07-structs-and-enums/distinct_equatable_impl/distinct_equatable_impl_test.nomi",
		"09-functions-and-control-flow/field_access_and_predicates_test.nomi",
		"09-functions-and-control-flow/tail_calls_test.nomi",
		"11-interfaces-and-impls/module_qualified_impl_dispatch/module_qualified_impl_dispatch_test.nomi",
		"13-iterators-and-pipes/string_iterators_test.nomi",
		"14-modules-and-packaging/dotted_type_names/dotted_type_names_test.nomi",
		// `//!` prompt cases.
		"02-testing/attached_tests_test.nomi",
		// Map patterns and destructuring (Map.get, a Some test
		// and the value's pattern), composite map keys, literal-attach
		// patterns over an enum, a user enum carrying maps and lists of
		// itself, and the Map.keys / values / map_values intrinsics.
		"01-foundations/comment_positions_test.nomi",
		"06-collections/maps_test.nomi",
		"08-pattern-matching/compound_patterns_test.nomi",
		"08-pattern-matching/map_patterns_test.nomi",
		"08-pattern-matching/variant_literal_attach_test.nomi",
		"18-ffi-and-dynamic/json_test.nomi",
		// Enum field reads (ProjEnumField), struct-variant
		// field sub-patterns, Set algebra and
		// Iter.to_set, signalling take_while and the remaining Iter
		// adapters, nested tuple component patterns and Map.values/remove.
		"06-collections/sets_test.nomi",
		"07-structs-and-enums/enums_test.nomi",
		"13-iterators-and-pipes/iterator_inference_test.nomi",
		"13-iterators-and-pipes/take_drop_test.nomi",
		"15-app-and-defer/file_store_pattern_test.nomi",
		// Operands typed by a call's solved signature, and an
		// untyped empty list passed to a std host.
		"08-pattern-matching/maybe_result_test.nomi",
		"04-scalars-and-text/strings_test.nomi",
		// Vector's std impls and `v == #[]`, Float and
		// Decimal literal patterns, typed tuple seeds, interface calls and
		// interpolation over std containers and a std error, a sibling
		// Comparable for Iter.sort and sort_by with a Direction, `if c {
		// break }` and bare `return` callbacks and Iter.loop in a subject.
		"06-collections/lists_test.nomi",
		"06-collections/vectors_test.nomi",
		"08-pattern-matching/case_patterns_test.nomi",
		"09-functions-and-control-flow/closures_and_lambdas_test.nomi",
		"11-interfaces-and-impls/stdlib_error_display/stdlib_error_display_test.nomi",
		"12-derives-and-standard-interfaces/collections_test.nomi",
		"13-iterators-and-pipes/constrained_sort/constrained_sort_test.nomi",
		"13-iterators-and-pipes/control_flow_test.nomi",
		"13-iterators-and-pipes/loop_control_test.nomi",
		// Distincts over tuples, maps and functions,
		// embedded distincts, derived impls over them, existential
		// parameters and a sibling type's interface impls.
		"10-generics-and-type-wrappers/embedded_variants_test.nomi",
		"10-generics-and-type-wrappers/type_wrappers_test.nomi",
		"12-derives-and-standard-interfaces/derives_test.nomi",
		"12-derives-and-standard-interfaces/universal_debug/universal_debug_test.nomi",
		"14-modules-and-packaging/same_named_types/same_named_types_test.nomi",
		// Bindings with `else`: blocks and arms that leave, fallbacks for a
		// payload, and an else in a test body.
		"08-pattern-matching/binding_else_test.nomi",
		// Codepoint literals as values, range ends and case patterns.
		"04-scalars-and-text/codepoint_literals_test.nomi",
	},
	"tour": {
		"bindings-and-expressions.md:L144",
		// A Result subject judged by shape.
		"testing.md:L27",
		// message_loop's counter server: nested variant patterns and Channel
		// fields.
		"concurrency.md:L1118",
		"testing.md:L16",
		"testing.md:L158",
		// `assert [left, right] = [10, 32]` takes a list literal.
		"testing.md:L54",
		// Walk-only bodies: a pipe of qualified String calls as an assertion
		// subject, whose stages record their rows by the walked route.
		"pipes.md:L165",
		"testing.md:L138",
		// A tagged literal in a walk-only body, and a `try` ending the case.
		"testing.md:L65",
		"testing.md:L79",
		// `//!` prompt cases.
		"bindings-and-expressions.md:L164",
		"testing.md:L95",
		"testing.md:L112",
		"testing.md:L123",
		// A group whose `boot` line calls the block's own entry boot, beside
		// the `main` the block runs first.
		"testing.md:L191",
		// Two groups calling a boot whose Startup parameter has a default:
		// one with no argument, one with a Startup.
		"testing.md:L232",
	},
	"failure": {
		// A nested record in an interpolation hole, a List of a struct
		// carrying a list, and List.compare over two untyped `[]`.
		"anon_struct.nomi",
		"inspect_through_list.nomi",
		"untyped_list_report.nomi",
		// `dbg` over an opaque type, a function value and an annotated
		// discard.
		"dbg_surfaces.nomi",
		// A binding declared at an interface type.
		"erased_operand_row.nomi",
		// Context values, and `if`/`case` in an assertion subject, whose
		// scrutinee and arm rows the VM appends to the report.
		"context_values_report.nomi",
		// A Decimal Set and Display over containers;
		// embedded distincts, existential parameters, callable distincts
		// and ordering operators.
		"decimal.nomi",
		"tests_inspect_vs_display.nomi",
		"debug_embeds.nomi",
		"debug_erased.nomi",
		"erased_operand.nomi",
		"func_operand_row.nomi",
		"ordering.nomi",
		"pipe_stage_keyword_report.nomi",
		"tests_case_subject.nomi",
		// A `try` that carried an AssertionFailure reports the check itself.
		"try_check_in_test.nomi",
		// A `try` inside a lambda leaves the lambda's own activation.
		"try_in_test.nomi",
		// Shape and Assertable subjects, and assertions in an ordinary fn
		// whose failure is the fn's Err.
		"assert_boundary.nomi",
		"assert_shape.nomi",
		"assertable_subject_report.nomi",
		"check_shape_report.nomi",
		// `if` without `else` is Unit, and Unit `==` answers after both
		// operands run; the failing case's rows match.
		"unit_value.nomi",
		// The fixture pipe.go names for this divergence: a piped call records
		// no `values:` rows and a directly written one does. See
		// internal/irbuild/irpipe.go.
		"pipe_assert_report.nomi",
		"tests_call_boundary.nomi",
		// A bare-name subject's defined-as block.
		"tests_defined_as.nomi",
		"tests_fail.nomi",
		"tests_group_failure.nomi",
		"tests_mixed.nomi",
		"tests_refute.nomi",
		"tests_short_circuit.nomi",
		"tests_trap.nomi",
		"virtual_clock_trap.nomi",
		// An enum field read the variant cannot answer is the case's fault.
		"enum_field_read_trap.nomi",
		// Lambda arguments retain in a test body, and a call through a
		// function value records its argument's row.
		"tests_lambda_boundary.nomi",
		// The operator interface written as a call and named by its type.
		"impl_call_operand_row.nomi",
		// A Bool singleton bound as a marker of its own type.
		"debug_display_surfaces.nomi",
		// A list pattern assertion takes a list literal.
		"pattern_assert.nomi",
		// Walk-only bodies: a cross-file file-qualified and type-qualified
		// call in an assertion subject records its arguments' rows.
		"siblings/report_test.nomi",
		"sibimpl/report_test.nomi",
		// Walk-only bodies take records and the scalar operator impl calls.
		"anon_vs_nominal.nomi",
		"scalar_operator_traps.nomi",
		// A bare `None` on either side of `==`.
		"untyped_equality_report.nomi",
		// Struct literals of generic and container-holding structs, and
		// equality on a std enum.
		"generic_struct.nomi",
		"std_enum_modes.nomi",
		"tests_operands.nomi",
		// `//!` prompt cases, including a failing one and a `try` that ends
		// one early.
		"tests_attached.nomi",
		"try_attached.nomi",
		// An Iter as a list element, and a qualified call inside an ordinary
		// fn's `testing.check` subject recording its rows.
		"iter_operand_report.nomi",
		"pipe_stages.nomi",
	},
}

// vmReportRoots are the populations whose record ids are paths, with the
// directory they are relative to.
//
// `stdlib` is absent because a stdlib record's id is a MODULE NAME and its
// cases are lowered by `irbuild.GenerateStdlibTestIR` rather than through
// `irbuild.Analyze`. runtime's TestExpectation_StdlibOnTheVM compares them.
var vmReportRoots = map[string]string{
	"corpus":  "tests",
	"failure": "internal/irbuild/testdata",
}

// reportShapedIDs is the records whose transcript IS a test report, with the
// count so a caller can pin it.
//
// THE FILTER IS A PROPERTY OF THE RECORD AND NOT OF ANY RUN, which is what
// keeps this from selecting the cases it passes: `N` is read out of the
// committed artifact before the machine is opened.
func reportShapedIDs(s *expectation.Set) (ids []string, cases map[string]expectation.Case) {
	cases = map[string]expectation.Case{}
	for _, c := range s.Cases {
		if c.Cases == 0 {
			continue
		}
		ids = append(ids, c.ID)
		cases[c.ID] = c
	}
	sort.Strings(ids)
	return ids, cases
}

// vmReportRun is one file's whole test report: lower it, find the module the
// entry file produced, and run every case it retained.
//
// It answers a normalized transcript plus the exit status and case count a
// record carries, or the reason this record is not comparable. THE REASONS ARE
// THE DELIVERABLE: "retained 2 of 6 cases" over a corpus file is a producer
// measurement, and reporting it per record is what makes a zero actionable
// instead of merely true.
// vmReportCaptureStderr runs fn with the process's stderr captured.
//
// rt writes a supervised task's failure report — `nomi: background task
// errored (attempt 1): … — retrying in …` — to os.Stderr, where the compiled
// program writes it too, and the recorder's transcript carries it under a
// `--- stderr ---` label. So the VM's report is compared with that stream
// included, or a program whose worker restarts could never match. The package
// runs no test in parallel, so the swap is observed by this run alone.
func vmReportCaptureStderr(fn func() (int, string)) (string, int, string) {
	r, w, err := os.Pipe()
	if err != nil {
		exit, reason := fn()
		return "", exit, reason
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	exit, reason := fn()
	os.Stderr = saved
	_ = w.Close()
	captured := <-done
	_ = r.Close()
	return captured, exit, reason
}

func vmReportRun(population, path string, wantCases int) (transcript string,
	exit, cases int, reason string) {
	prog, err := irbuild.Analyze(path)
	if err != nil {
		return "", 0, 0, "the front end refuses this program, so the record is a diagnostic"
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		return "", 0, 0, "Generate refuses this program"
	}
	var mod *ir.Module
	for _, m := range res.IR {
		// BY THE DECLARING PATH, which is what `ir.NewModule` is handed and
		// what `nomi test <file>` selects on: the command collects the NAMED
		// file's cases and no others, and `importedModuleRefusal` keeps the
		// builder's test table identical to it. A sibling module's tests are not
		// this record's.
		if m.Name() == path {
			mod = m
		}
	}
	if mod == nil {
		return "", 0, 0, "this file retained nothing at all, so it has no module"
	}
	if population == "tour" {
		// A TOUR RECORD'S N IS A FLAG, NOT A COUNT: `runTourBlock`'s set
		// records 1 for any block that declares tests. So a block of two
		// cases with one retained would pass a comparison against N and then
		// run half the block. The block's own case count is the runner's,
		// taken from the entry the front end checked, and the flag is what
		// the record is compared against.
		flag := wantCases
		wantCases = len(frontend.CollectTestCases(prog.Entry().Nodes))
		defer func() {
			if reason == "" {
				cases = flag
			}
		}()
	}
	if got := len(mod.Tests()); got != wantCases {
		return "", 0, 0, fmt.Sprintf("retained %d of %d case(s)", got, wantCases)
	}
	var out bytes.Buffer
	// THE PROGRAM'S MODULES, as `vmRun` hands a whole program: a std host
	// outside rt (calendar parsing, regex) is answered through its generated
	// adapter, and a machine opened without the linked modules reports its
	// own limit instead of the case.
	mod, mods, err := vmLink(mod, res.IRModules())
	if err != nil {
		return "", 0, 0, "the link hook refused it: " + err.Error()
	}
	m := vm.NewProgram(mod, mods, &out)
	if population != "tour" {
		var stderr string
		stderr, exit, reason = vmReportCaptureStderr(func() (int, string) { return m.RunTests(path) })
		if reason != "" {
			return "", 0, 0, "the machine could not run a case: " + reason
		}
		// The recorder's transcript: stdout, then stderr under a label when
		// anything reached it (internal/expectation's `observed.transcript`).
		transcript = out.String()
		if stderr != "" {
			transcript += "--- stderr ---\n" + stderr
		}
		return transcript, exit, len(mod.Tests()), ""
	}
	// THE TOUR'S COMPOSITION IS THE PLAYGROUND'S AND IT IS NOT `nomi test`'s,
	// in two ways, both read off `runTourBlock` in
	// internal/expectation/populations_test.go rather than guessed:
	//
	//  1. THE PROGRAM RUNS FIRST, into the same buffer, and then the tests.
	//     `nomi test <file>` lowers `fn main` and never calls it
	//     (`renderTestBoot`), so the two commands produce different
	//     transcripts for one file and the record is the playground's.
	//  2. A CASE IS LABELLED BARE. The playground builds a
	//     `vmhost.NewTestReport` and calls `rep.Result(c.Name, …)` with no
	//     path, because a block has no file a reader could open.
	//
	// The second is why `rt.RunTestsLabelled` has a label parameter, and the
	// evidence that it is the WHOLE difference is a digest: with
	// `rt.TestName`'s label neither of the two reachable blocks matched, and
	// with the bare label both matched the committed record exactly. See
	// TestVMReport_TheTourLabelIsTheWholeDifference.
	for _, f := range mod.Funcs() {
		if f.Name() != "main" || len(f.Params()) != 0 {
			continue
		}
		// The program runs as the playground's run does: boot when the
		// block's entry declares one, `main`, then the app's shutdown.
		if err := m.Main(context.Background(), nil, false); err != nil {
			return "", 0, 0, "the machine ran part of the block's program: " + err.Error()
		}
		break
	}
	exit, reason = m.RunTestsLabelled(func(name string) string { return name })
	if reason != "" {
		return "", 0, 0, "the machine could not run a case: " + reason
	}
	return out.String(), exit, len(mod.Tests()), ""
}

// vmReportSubsetOf runs every report-shaped record and returns the engine's
// Set plus a per-record refusal report.
func vmReportSubsetOf(t *testing.T, population string, ids []string,
	recorded map[string]expectation.Case,
	pathFor func(string) (string, bool)) (*expectation.Set, []string) {
	t.Helper()
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	got := &expectation.Set{Population: population,
		What: "one record per file whose every `test` case is a retained ir.Func, run " +
			"on internal/vm through rt.RunTestsTo and compared as a SUBSET of the " +
			"committed population."}
	var refused []string
	for _, id := range ids {
		path, ok := pathFor(id)
		if !ok {
			refused = append(refused, fmt.Sprintf("%s: could not be staged", id))
			continue
		}
		transcript, exit, cases, reason := vmReportRun(population, path, recorded[id].Cases)
		if reason != "" {
			refused = append(refused, fmt.Sprintf("%s: %s", id, reason))
			continue
		}
		got.Add(expectation.NewCase(id, exit, cases, expectation.Normalize(transcript, root)))
	}
	got.Sort()
	return got, refused
}

// vmReportPathResolver answers, for a record id, the entry `.nomi` file to
// lower.
//
// The tour's staging is `vmPathResolver`'s, CALLED rather than copied: a
// block's id is `chapter:L<line>` and only the markdown holds the code, and a
// fourth copy of the staging is what `expectation_test.go` already names as a
// drift risk.
func vmReportPathResolver(t *testing.T, population string) func(string) (string, bool) {
	t.Helper()
	if population == "tour" {
		return vmPathResolver(t, "tour")
	}
	root, err := expectation.Root()
	if err != nil {
		t.Fatalf("resolving the worktree root: %v", err)
	}
	rel, known := vmReportRoots[population]
	if !known {
		t.Fatalf("no root for population %q", population)
	}
	base := filepath.Join(root, rel)
	return func(id string) (string, bool) { return filepath.Join(base, id), true }
}

// TestVMReport_TheVMIsComparedAgainstTheReportShapedRecords is the runner's
// acceptance: four populations enumerated, every report-shaped record run, the
// result compared with `CompareSubset`, and every count pinned.
func TestVMReport_TheVMIsComparedAgainstTheReportShapedRecords(t *testing.T) {
	total, shaped, compared := 0, 0, 0
	for _, population := range []string{"corpus", "failure", "tour", "stdlib"} {
		pins := vmReportPopulations[population]
		set, err := expectation.Load(population)
		if err != nil {
			t.Fatalf("%s: loading the committed artifact: %v", population, err)
		}
		ids, recorded := reportShapedIDs(set)
		total += len(set.Cases)
		shaped += len(ids)
		if len(set.Cases) != pins.records(population) {
			t.Errorf("%s: %d records, the population table says %d",
				population, len(set.Cases), pins.records(population))
		}
		if len(ids) != pins.reportShaped(population) {
			t.Errorf("%s: %d report-shaped records, the population table says %d",
				population, len(ids), pins.reportShaped(population))
		}
		if population == "stdlib" {
			// See vmReportRoots: this population's zero is established by the
			// decline that produces it rather than by a run it cannot have.
			compared += pins.comparable
			continue
		}
		got, refused := vmReportSubsetOf(t, population, ids, recorded,
			vmReportPathResolver(t, population))
		if diffs := set.CompareSubset(got); len(diffs) > 0 {
			t.Errorf("%s: the VM disagrees with the committed expectation in %d place(s):\n    %s",
				population, len(diffs), strings.Join(diffs, "\n    "))
		}
		if len(got.Cases) != pins.comparable {
			t.Errorf("%s: %d comparable record(s), pinned at %d. A FALL is the regression this "+
				"pin exists for: CompareSubset reports nothing about a subset that shrank.\n"+
				"    compared: %s", population, len(got.Cases), pins.comparable,
				strings.Join(reportIDs(got), " "))
		}
		compared += len(got.Cases)
		for _, r := range refused {
			t.Log("    " + r)
		}
	}
	// THE TOTALS ARE DERIVED, and so are the per-population counts they sum:
	// `vmPopulations` in expectation_test.go is the single owner, and this
	// unit contributes only `comparable`, which is its own measurement.
	wantTotal, wantShaped := 0, 0
	for _, population := range []string{"corpus", "failure", "tour", "stdlib"} {
		wantTotal += vmPopulations[population].records()
		wantShaped += vmPopulations[population].reportShaped
	}
	if total != wantTotal {
		t.Errorf("the four artifacts hold %d records, the table says %d", total, wantTotal)
	}
	if shaped != wantShaped {
		t.Errorf("%d records are report-shaped, the table says %d", shaped, wantShaped)
	}
	// THE FRACTION OF THE WHOLE ARTIFACT SET FIRST, because the report-shaped
	// denominator flatters.
	t.Logf("report-shaped records compared: %d of %d (%.1f%%), %d of the %d report-shaped",
		compared, wantTotal, 100*float64(compared)/float64(wantTotal), compared, wantShaped)
}

// reportIDs is a Set's ids, for logging.
func reportIDs(s *expectation.Set) []string {
	out := make([]string, 0, len(s.Cases))
	for _, c := range s.Cases {
		out = append(out, c.ID)
	}
	return out
}

// TestVMReport_TestCasesAreRetainedApartFromDeclarations checks that a file's
// test cases are retained as test cases, apart from its declarations.
//
// It counts how many of the file's retained `ir.Func`s are DECLARATIONS
// (`Module.Funcs()`) and how many are test CASES (`Module.Tests()`). A runner
// built over `Funcs()` alone would have nothing to drive, and a test case
// must not be resolvable as a callee.
func TestVMReport_TestCasesAreRetainedApartFromDeclarations(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "internal/irbuild/testdata/tests_fail.nomi")
	prog, err := irbuild.Analyze(path)
	if err != nil {
		t.Fatalf("analyzing %s: %v", path, err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("generating %s: %v", path, err)
	}
	var decls, cases int
	for _, m := range res.IR {
		decls += len(m.Funcs())
		cases += len(m.Tests())
		for _, c := range m.Tests() {
			for _, f := range m.Funcs() {
				if f == c.Fn() {
					t.Errorf("%s is in BOTH Funcs() and Tests(); a test case is not a "+
						"declaration and must not be resolvable as a callee", c.Name())
				}
			}
		}
	}
	if cases == 0 {
		t.Fatal("no test case was retained for tests_fail.nomi, so the runner has " +
			"nothing to drive and the whole comparison above is vacuous")
	}
	if decls == 0 {
		t.Error("tests_fail.nomi declares `fn double`, so a declaration should be " +
			"retained too; a zero here means the fixture stopped exercising the call path")
	}
	t.Logf("tests_fail.nomi: %d retained declaration(s), %d retained test case(s)", decls, cases)
}

// TestVMReport_AttachedCasesAreRetained holds a file with both case kinds to
// retaining every case: `testdata/tests_attached.nomi` declares one `test`
// declaration and three `//!` prompt cases, and a producer that declined the
// prompts would retain one.
func TestVMReport_AttachedCasesAreRetained(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set, err := expectation.Load("failure")
	if err != nil {
		t.Fatal(err)
	}
	var declared int
	for _, c := range set.Cases {
		if c.ID == "tests_attached.nomi" {
			declared = c.Cases
		}
	}
	if declared == 0 {
		t.Fatal("tests_attached.nomi is not in the failure population any more, so this " +
			"control has no witness; find another file with both case kinds")
	}
	path := filepath.Join(root, "internal/irbuild/testdata/tests_attached.nomi")
	prog, err := irbuild.Analyze(path)
	if err != nil {
		t.Fatalf("analyzing: %v", err)
	}
	res, _, err := irbuild.GenerateIR(prog)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	retained := 0
	for _, m := range res.IR {
		if m.Name() == path {
			retained = len(m.Tests())
		}
	}
	if retained != declared {
		t.Errorf("%d of %d cases of tests_attached.nomi retained; its `//!` prompts are "+
			"ordinary test bodies and every one should be", retained, declared)
	}
}

// TestVMReport_APlantedWrongFailureMessageIsCaught is a control about the
// MESSAGE rather than about a value.
//
// WHY A MESSAGE AND NOT A VALUE. `expectation_test.go`'s plant appends text to
// every line of a program's output, which shows the digest reaches the
// artifact. It says nothing about the one thing this population can do that
// the other four cannot: catch a report that is WRONG while every value in the
// program is right, such as `*rt.AssertionFailure.Error()` printing only its
// header.
//
// SO THE PLANT DELETES THE `values:` ROWS, which is a row going MISSING: the
// verdict is unchanged, the exit status is unchanged, every case still fails
// where it failed, and the only difference is that the report no longer
// explains why. It travels the real `Normalize` -> `NewCase` ->
// `CompareSubset` path, and `compareInto`'s failure branch prints BOTH texts —
// which is the whole reason a red record stores its transcript instead of a
// hash.
//
// AND IT DISCRIMINATES. Exactly the records whose report PRINTS ROWS must be
// reported and every other comparable record must be silent — which includes
// two that FAIL and print none, because `1 == 2`'s operands read exactly like
// their values and `rt.RecordOperand` drops them. A plant that changed every
// record would prove the comparison sees SOMETHING, not that it sees the rows.
func TestVMReport_APlantedWrongFailureMessageIsCaught(t *testing.T) {
	vmReportPlant(t, "deleting the `values:` rows", vmReportWithValueRows,
		nil,
		func(normalized string) string { return dropValueRows(normalized) })
}

// vmReportPlant is the shared shape of both message plants: run every
// comparable record clean, require the clean run to AGREE, then run it again
// under a mutation and require exactly the named records to be reported.
//
// ONE FUNCTION FOR BOTH, because the two plants differ only in where the
// mutation lives — `install` for a mutation inside `rt`, `bend` for one
// applied to the transcript — and a second copy of the clean-run control is a
// second chance to forget it.
func vmReportPlant(t *testing.T, what string, mustReport []string,
	install func() func(), bend func(string) string) {
	t.Helper()
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, id := range mustReport {
		named[id] = true
	}
	for _, population := range []string{"corpus", "failure", "tour"} {
		set, err := expectation.Load(population)
		if err != nil {
			t.Fatalf("%s: %v", population, err)
		}
		ids, recorded := reportShapedIDs(set)
		pathFor := vmReportPathResolver(t, population)
		// THE BEND IS APPLIED TO THE TRANSCRIPT BEFORE `NewCase`, because
		// `NewCase` computes the digest and the failing flag from the text it
		// is given; bending `Case.Transcript` afterwards would leave both
		// describing the unbent text.
		run := func(bend func(string) string) *expectation.Set {
			out := &expectation.Set{Population: population}
			for _, id := range ids {
				path, _ := pathFor(id)
				transcript, exit, cases, reason := vmReportRun(population, path,
					recorded[id].Cases)
				if reason != "" {
					continue
				}
				normalized := expectation.Normalize(transcript, root)
				if bend != nil {
					normalized = bend(normalized)
				}
				out.Add(expectation.NewCase(id, exit, cases, normalized))
			}
			out.Sort()
			return out
		}
		clean := run(nil)
		want := vmReportComparable[population]
		if len(clean.Cases) != len(want) {
			t.Fatalf("%s: %d comparable record(s) but %d named in vmReportComparable; "+
				"the control's own subject moved", population, len(clean.Cases), len(want))
		}
		// THE CONTROL HAS ITS OWN CONTROL: the unmutated run must agree
		// first, or a reported difference below would prove nothing.
		if diffs := set.CompareSubset(clean); len(diffs) > 0 {
			t.Fatalf("%s: the UNMUTATED run already disagrees, so the plant proves "+
				"nothing:\n    %s", population, strings.Join(diffs, "\n    "))
		}
		restore := func() {}
		if install != nil {
			restore = install()
		}
		planted := run(bend)
		restore()
		// ONE RECORD AT A TIME, because CompareSubset bounds how many
		// differences it reports: with more mutated records than the bound,
		// the ones sorted last would read as unreported.
		reported := map[string]bool{}
		for _, c := range planted.Cases {
			one := &expectation.Set{Population: planted.Population, What: planted.What}
			one.Add(c)
			for _, d := range set.CompareSubset(one) {
				if strings.HasPrefix(d, c.ID+":") {
					reported[c.ID] = true
				}
			}
		}
		for _, id := range want {
			switch {
			case named[id] && !reported[id]:
				t.Errorf("%s: %s was NOT reported for %s, so that part of the failure "+
					"report is invisible to this comparison", population, what, id)
			case !named[id] && reported[id]:
				t.Errorf("%s: %s reports nothing %s could have changed, so the "+
					"comparison is reporting something other than what was planted",
					population, id, what)
			}
		}
	}
}

// dropValueRows removes the `values:` block from every failure report in a
// transcript, leaving every other line byte-identical.
//
// THE ROWS ARE INDENTED UNDER THE LABEL and the label is `rt`'s own
// (`writeAssertionReport`), so the plant is keyed on the label and on the
// deeper indentation that follows it rather than on any text the machine
// produced.
func dropValueRows(transcript string) string {
	lines := strings.Split(transcript, "\n")
	out := make([]string, 0, len(lines))
	dropping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "values:" {
			dropping = true
			continue
		}
		if dropping {
			// The block ends at the first line that is not more deeply
			// indented than the label was.
			if strings.HasPrefix(line, "        ") {
				continue
			}
			dropping = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestVMReport_APlantedWrongSourceFragmentIsCaught is the second message
// plant, and the difference from the one above is WHERE THE MUTATION LIVES.
//
// The plant above bends the transcript after the run, which shows that
// `CompareSubset` and the digesting reach the failure text. THIS ONE MUTATES
// INSIDE `rt`'s OWN REPORT LAYOUT: `rt.Highlight` is the hook
// `internal/termcolor`'s `init` installs in the `nomi` binary, and
// `rt.nomiSource` passes every Nomi source fragment a failure report
// quotes through it. So the plant travels the real route —
// `Machine.RunTests` -> `rt.RunTestsTo` -> the reporter -> `writeAssertion
// Report` -> `nomiSource` -> the bytes the machine's writer received.
//
// THE HOOK IS A REAL SEAM AND NOT A TEST BACK DOOR, which is worth saying
// because it decides whether this control measures the shipped path.
// `rt.Highlight` is nil in an embedding that does not link `internal/termcolor`
// and non-nil under `nomi run`,
// so both states are real; this installs a third function into the same slot.
//
// IT DISCRIMINATES FOR A SECOND REASON. Only a report that QUOTES Nomi source
// has a fragment to mutate, so `tests_trap.nomi` — whose report is one
// `line N: integer overflow` header with no assertion behind it — must come
// through untouched, as must every passing record, even though the hook is
// installed for their runs too.
func TestVMReport_APlantedWrongSourceFragmentIsCaught(t *testing.T) {
	vmReportPlant(t, "a mutation inside rt's own report layout",
		vmReportWithSourceFragment,
		func() func() {
			prev := rt.Highlight
			rt.Highlight = func(_ io.Writer, src string) string { return src + " <PLANTED>" }
			return func() { rt.Highlight = prev }
		}, nil)
}

// TestVMReport_TheTourLabelIsTheWholeDifference is the evidence behind
// `rt.RunTestsLabelled` existing at all, and it is a DIGEST rather than an
// argument.
//
// The tour playground reports a case under its bare name and `nomi test
// <file>` reports `<path> :: <case>`. Nothing about the rest of the report
// differs, and that claim is checkable: run a tour block's cases both ways and
// ask which digest the committed record holds. WITH `rt.TestName`'s LABEL
// NEITHER MATCHES; WITH THE BARE LABEL BOTH MATCH EXACTLY.
//
// IT FAILS IN THE DIRECTION A PRICING CLAIM SHOULD. If the path-labelled run
// ever matched, the two compositions would not differ and the parameter would
// be ceremony. If the bare-labelled run stopped matching, something else about
// the tour's report differs too and this file's reading of it is wrong.
func TestVMReport_TheTourLabelIsTheWholeDifference(t *testing.T) {
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	set, err := expectation.Load("tour")
	if err != nil {
		t.Fatal(err)
	}
	recorded := map[string]expectation.Case{}
	for _, c := range set.Cases {
		recorded[c.ID] = c
	}
	pathFor := vmPathResolver(t, "tour")
	// NAMED RATHER THAN DERIVED, for `vmLinkedRecords`' reason: a list computed
	// by "which tour records the VM can run" would be computed by the very
	// mechanism under test.
	for _, id := range []string{"bindings-and-expressions.md:L144", "testing.md:L16"} {
		path, ok := pathFor(id)
		if !ok {
			t.Fatalf("%s could not be staged", id)
		}
		prog, err := irbuild.Analyze(path)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		res, _, err := irbuild.GenerateIR(prog)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		var mod *ir.Module
		for _, m := range res.IR {
			if m.Name() == path {
				mod = m
			}
		}
		if mod == nil || len(mod.Tests()) != recorded[id].Cases {
			t.Fatalf("%s: the block no longer retains its %d case(s), so this control has "+
				"no subject", id, recorded[id].Cases)
		}
		digest := func(label func(string) string) string {
			var out bytes.Buffer
			m := vm.NewProgram(mod, res.IRModules(), &out)
			if _, reason := m.RunTestsLabelled(label); reason != "" {
				t.Fatalf("%s: %s", id, reason)
			}
			return expectation.Digest(expectation.Normalize(out.String(), root))
		}
		bare := digest(func(name string) string { return name })
		withPath := digest(func(name string) string { return rt.TestName(path, name) })
		if bare != recorded[id].Digest {
			t.Errorf("%s: the BARE label does not reproduce the committed record "+
				"(got %s, recorded %s); the tour's report differs from `nomi test`'s in "+
				"more than the label and this file's pricing of it is wrong",
				id, bare[:16], recorded[id].Digest[:16])
		}
		if withPath == recorded[id].Digest {
			t.Errorf("%s: `rt.TestName`'s label reproduces the record too, so the two "+
				"compositions do not differ and rt.RunTestsLabelled's parameter is "+
				"ceremony", id)
		}
	}
}
