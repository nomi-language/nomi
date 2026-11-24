package irbuild

import (
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/std"
)

// TestStdIntrinsicOwners_EveryFunctionHasItsArm holds stdIntrinsicOwners to
// std's declarations: each owner's module declares an inherent block for
// it, each member an entry names is a function of one, and every function
// of an owner whose arm lowers all of them is one its arm's table lists
// (channelFuncs, taskFuncs), since the arm panics on any other.
func TestStdIntrinsicOwners_EveryFunctionHasItsArm(t *testing.T) {
	lib := std.Load()
	for _, o := range stdIntrinsicOwners {
		nodes := lib.Nodes[strings.TrimPrefix(o.owner.Module, "std/")]
		// hostFns are the ones an arm lowers: a function std writes in
		// Nomi takes the ordinary route (intrinsicArm), so the arm's table
		// owes it no row.
		var fns, hostFns []string
		for _, n := range nodes {
			impl, isImpl := n.(*ast.ImplBlock)
			if !isImpl || impl.Interface != nil || analysis.TypeExprBaseName(impl.Receiver) != o.owner.Name {
				continue
			}
			for _, item := range impl.Items {
				switch fn := item.(type) {
				case *ast.ExternFunc:
					fns = append(fns, fn.Name)
					hostFns = append(hostFns, fn.Name)
				case *ast.FuncDef:
					fns = append(fns, fn.Name)
				}
			}
		}
		if len(fns) == 0 {
			t.Errorf("%s declares no inherent function of %s, so its arm lowers nothing", o.owner.Module, o.owner.Name)
			continue
		}
		for _, m := range o.members {
			if !slices.Contains(fns, m) {
				t.Errorf("%s declares no %s.%s, which stdIntrinsicOwners lists", o.owner.Module, o.owner.Name, m)
			}
		}
		if o.members != nil {
			continue
		}
		for _, name := range hostFns {
			switch o.arm {
			case armChannel:
				if _, ok := channelFuncs[o.owner.Name+"."+name]; !ok {
					t.Errorf("channelCall lowers every function of %s, and channelFuncs has no row for %s.%s", o.owner.Name, o.owner.Name, name)
				}
			case armTask:
				if _, ok := taskFuncs[name]; !ok {
					t.Errorf("taskCall lowers every function of Task, and taskFuncs has no row for Task.%s", name)
				}
			default:
				t.Errorf("stdIntrinsicOwners gives %s's arm every function of %s, and that arm lowers only the ones it names", o.owner.Name, o.owner.Name)
			}
		}
	}
}

// A program's own type named as an intrinsic owner is called as written:
// the arm is the one the call's record names, and this record names the
// program's function.
func TestIntrinsicArm_AProgramsTypeOfAnIntrinsicOwnersName(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Channel
  name: String
end
impl Channel
  fn send(c: Channel, text: String) -> String
    "${c.name}: ${text}"
  end
  fn buffered(n: Int) -> Channel
    Channel{name: "c${n}"}
  end
end
fn main()
  c = Channel.buffered(3)
  io.print(Channel.send(c, "hi"))
end`, "c3: hi\n")
}

// An intrinsic owner imported under another name reaches its arm: the
// record names std's function whatever the owner is spelled.
func TestIntrinsicArm_AnOwnerImportedUnderAnotherName(t *testing.T) {
	verifyLambdaProgram(t, `import
  std::channels.{Channel as C, Receiver as R, Sender as S}
  std::io
end
fn main()
  ch = C.buffered<Int>(2)
  _ = S.send(ch.sender, 7)
  io.print(R.receive(ch.receiver))
end`, "Some(7)\n")
}
