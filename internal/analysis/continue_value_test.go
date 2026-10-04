package analysis_test

import (
	"strings"
	"testing"
)

// `continue` takes no value. Inside `Iter.loop`, `continue (i + 1, s)` parsed
// as a `continue` followed by an unreachable tuple, so the loop went round on
// the unchanged state forever. It is now a value the checker rejects.
func TestContinueValue_IsRejectedWithTheWorkingForm(t *testing.T) {
	const want = "continue takes no value; it goes to the next iteration with the loop state or accumulator unchanged. " +
		"To carry a new value forward, make it the callback's result: write it as the last expression, without continue " +
		"(in Iter.loop that result is the next state)"
	for _, body := range []string{
		`r = Iter.loop(|p = (0, 0)| {
    (i, s) = p
    if i >= 5 {
      break p
    }
    continue (i + 1, s + i)
  })
  _ = r`,
		`r = [1, 2] |> Iter.reduce(|acc = 0, x| {
    if x > 1 {
      continue acc
    }
    acc + x
  })
  _ = r`,
	} {
		_, errs := checkSourceWithStdlib("fn main() {\n  " + body + "\n}\n")
		found := false
		var got []string
		for _, e := range errs {
			got = append(got, e.Message)
			if strings.Contains(e.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("want the continue-value error, got %v\n%s", got, body)
		}
	}
}

func TestContinueValue_BareContinueIsAccepted(t *testing.T) {
	for _, body := range []string{
		`r = Iter.loop(|n = 0| {
    if n < 3 { continue }
    break n
  })
  _ = r`,
		`xs = [1, 2] |> Iter.map(|x| {
    if x > 1 { continue }
    x
  }) |> Iter.to_list()
  _ = xs`,
		`r = [1, 2] |> Iter.reduce(|acc = 0, x| {
    case x {
      1 -> continue
      _ -> acc + x
    }
  })
  _ = r`,
	} {
		_, errs := checkSourceWithStdlib("fn main() {\n  " + body + "\n}\n")
		for _, e := range errs {
			if strings.Contains(e.Message, "continue takes no value") {
				t.Errorf("a bare continue was rejected: %v\n%s", e.Message, body)
			}
		}
	}
}
