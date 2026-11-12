package analysis_test

import (
	"strings"
	"testing"
)

const senderDecls = `interface Sender
    fn send(sender: self, message: String) -> Int
end

struct Null end

`

// An impl parameter the body never reads takes the discard form of the
// interface's name: `_message` matches `message`, and is not reported as
// unread. Any other name is still the mismatch.
func TestImplDiscardParam_MatchesTheInterfaceName(t *testing.T) {
	accepted := senderDecls + `impl Sender for Null
    fn send(_null: Null, _message: String) -> Int
        0
    end
end

fn main()
    dbg Sender.send(Null{}, message: "x")
end
`
	if _, errs := checkSourceWithStdlib(accepted); len(errs) > 0 {
		t.Fatalf("`_message` for `message` should be accepted, got %v", errs)
	}

	for _, name := range []string{"_msg", "_", "message_"} {
		src := senderDecls + `impl Sender for Null
    fn send(_null: Null, ` + name + `: String) -> Int
        0
    end
end
`
		got := implErrors(src)
		want := "impl function 'send': parameter name '" + name + "' does not match interface declaration 'message'"
		if len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("%s: got %v, want one error %q", name, got, want)
		}
	}
}
