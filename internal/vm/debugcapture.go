package vm

import (
	"errors"
	"path/filepath"
	"strconv"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Captured is one `io.capture` or `io.replay` call in force where a stopped
// frame is: what it has captured so far, which a debugger shows because
// nothing written inside the call reaches the program's output while it runs.
type Captured struct {
	// Label names the call and where the program made it:
	// `io.replay at bank.nomi:14`.
	Label string
	// Site is where the program made the call, or the zero Pos.
	Site ir.Pos
	rt.CaptureView
}

// Captured are the `io.capture` and `io.replay` calls in force at a frame
// Stack handed out, innermost first. It reads what the machine holds and runs
// nothing.
//
// A capture rides the rt frame an activation runs on, so the frames inside a
// call carry it and the frame that made the call does not. The call's site is
// where the activation that called `io.capture` or `io.replay` is: the parent
// of the innermost activation outside the capture, which is std/io's own
// `capture` or `replay`. A tail call to it shows its caller's site instead,
// and a task spawned inside the call, whose stack starts in the task, names
// no site.
func (d *Debugger) Captured(frameID int) ([]Captured, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if frameID < 1 || frameID > len(d.frames) {
		return nil, errors.New("vm: no frame " + strconv.Itoa(frameID) + "; the program resumed")
	}
	ref := d.frames[frameID-1]
	if ref.via || ref.fr == nil {
		return nil, nil
	}
	var out []Captured
	for _, c := range ref.fr.runtime.OpenCaptures() {
		view := c.View()
		label := "io.capture"
		if view.Replay {
			label = "io.replay"
		}
		outside := ref.fr
		for outside != nil && outside.runtime.Carries(c) {
			outside = outside.parent
		}
		var site ir.Pos
		if outside != nil && outside.parent != nil {
			if pos, _ := callerAt(outside.parent); pos.IsValid() && pos.File() != "" {
				label += " at " + filepath.Base(pos.File()) + ":" + strconv.Itoa(pos.Line())
				site = pos
			}
		}
		out = append(out, Captured{Label: label, Site: site, CaptureView: view})
	}
	return out, nil
}
