package irbuild

import "testing"

func TestIREmbeds_TourEventList(t *testing.T) {
	verifyLambdaProgram(t, `struct Click
  x: Int
  y: Int
end

struct KeyDown
  key: String
end

type FocusLost // bare — zero-sized

enum Event
  embeds Click
  embeds KeyDown
  embeds FocusLost
end

fn main()
  events: List<Event> = [Click{x: 10, y: 20}, KeyDown{key: "Enter"}, FocusLost]
  dbg events
end
`, "dbg line 20: events = [Click{x: 10, y: 20}, KeyDown{key: \"Enter\"}, FocusLost]\n")
}

// Embedded values widen at returns, arguments and annotated bindings; cases
// match them through positional, brace and bare patterns; a single Event
// renders through its derived Debug.
func TestIREmbeds_WideningCasesAndDebug(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

struct Click
  x: Int
  y: Int
end

struct KeyDown
  key: String
end

type FocusLost

enum Event
  embeds Click
  embeds KeyDown
  embeds FocusLost
end

fn click_at(x: Int) -> Event
  Click{x:, y: x + 1}
end

fn describe(e: Event) -> String
  case e
    Event.Click{x:, y:} -> "click ${x},${y}"
    Event.KeyDown(k) -> "key ${k.key}"
    Event.FocusLost -> "focus lost"
  end
end

fn main()
  a = click_at(3)
  dbg a
  io.print(describe(a))
  io.print(describe(KeyDown{key: "Esc"}))
  io.print(describe(FocusLost))
  lost: Event = FocusLost
  dbg lost
end
`, "dbg line 34: a = Click{x: 3, y: 4}\nclick 3,4\nkey Esc\nfocus lost\n"+
		"dbg line 39: lost = FocusLost\n")
}
