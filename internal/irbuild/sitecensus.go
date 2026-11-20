package irbuild

import (
	"fmt"
	"sync"
)

// siteCensus counts, while a test has it open, the distinct source sites
// of each class a verifier finds (noteUnreadOperator, verifyLocalRead,
// noteComparator, verifyExprKind, noteDebugImpl). Only a program's
// lowering is counted (a gen with a std instance table, not std's own:
// stdInstances.stdOwn): std's lowerings run once a process, or not at all
// when its bundle is read back, so counting them would make a count depend
// on what the artifact cache holds. A census with std set counts std's
// own lowerings instead, to be reported and never held.
// TestIRGolden_RecordsMatchTheVM opens each census and holds its classes
// to their maxima (checkSiteCensus).
type siteCensus struct {
	sync.Mutex
	std   bool
	open  bool
	seen  map[string]bool
	sites map[string][]string
}

var (
	operatorCensus    siteCensus
	localReadCensus   siteCensus
	comparatorCensus  siteCensus
	exprKindCensus    siteCensus
	stdExprKindCensus = siteCensus{std: true}
	debugImplCensus   siteCensus
)

// note counts the site (line, col) of the file g lowers under class.
func (c *siteCensus) note(g *gen, class string, line, col int) {
	if (g.stdInsts == nil || g.stdInsts.stdOwn) != c.std {
		return
	}
	c.Lock()
	defer c.Unlock()
	if !c.open {
		return
	}
	fa := g.srcFA()
	path := fa.FilePath
	if path == "" {
		path = fa.Origin
	}
	site := fmt.Sprintf("%s:%d:%d", path, line, col)
	key := class + "\x00" + site
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.sites[class] = append(c.sites[class], site)
}

// counting reports whether the census is open, for a caller whose class
// costs work to find.
func (c *siteCensus) counting() bool {
	c.Lock()
	defer c.Unlock()
	return c.open
}

// start starts counting and answers a function that stops and returns the
// distinct sites by class.
func (c *siteCensus) start() func() map[string][]string {
	c.Lock()
	c.open, c.seen, c.sites = true, map[string]bool{}, map[string][]string{}
	c.Unlock()
	return func() map[string][]string {
		c.Lock()
		defer c.Unlock()
		out := c.sites
		c.open, c.seen, c.sites = false, nil, nil
		return out
	}
}
