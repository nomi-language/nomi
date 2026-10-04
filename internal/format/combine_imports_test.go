package format

import (
	"testing"
)

func combineEq(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	again, err := Format(got)
	if err != nil {
		t.Fatalf("Format (idempotence): %v", err)
	}
	if again != got {
		t.Errorf("not idempotent:\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

// Distinct flat imports combine into one block (the headline case).
func TestCombine_DistinctFlatToBlock(t *testing.T) {
	combineEq(t,
		"import std/io\nimport std/structs: Struct\n\nfn main() {\n    42\n}\n",
		"import {\n    std/io\n    std/structs.Struct\n}\n\nfn main() {\n    42\n}\n")
}

// Two selective imports of the same module merge; collapsing to one statement
// then renders bare (the single-entry rule).
func TestCombine_SameModuleSelectiveMerge(t *testing.T) {
	combineEq(t,
		"import std/io.{print}\nimport std/io.{inspect}\n\nfn main() {\n    42\n}\n",
		"import std/io.{inspect, print}\n\nfn main() {\n    42\n}\n")
}

// Selective imports of the same module merge to one selector list.
func TestCombine_BarePlusSelective(t *testing.T) {
	combineEq(t,
		"import std/calendar.{Error}\nimport std/calendar.{Days}\n\nfn main() {\n    42\n}\n",
		"import std/calendar.{Days, Error}\n\nfn main() {\n    42\n}\n")
}

// Comments keep a visible boundary between import sections.
func TestCombine_CommentRelocation(t *testing.T) {
	combineEq(t,
		"// header\nimport std/io\n// middle\nimport std/lists: List\n\nfn main() {\n    42\n}\n",
		"// header\nimport std/io\n// middle\nimport std/lists.List\n\nfn main() {\n    42\n}\n")
}

// A single import stays bare (the count rule's other half).
func TestCombine_SingleStaysBare(t *testing.T) {
	combineEq(t,
		"import std/io\n\nfn main() {\n    42\n}\n",
		"import std/io\n\nfn main() {\n    42\n}\n")
}
