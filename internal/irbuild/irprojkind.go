package irbuild

import "github.com/nomi-language/nomi/internal/ir"

// irProjKindObserved is a test-only hook, nil in production, called with every
// `ir.Proj` this package constructs and with the result `kind` the producer had
// in hand at the time.
//
// It lets a test read two things no node holds: the population of
// projections per builder site, and a cross-check of `irParamShape(k)` here
// against `p.Shape()` on the node, two reads of one construction.
//
// It is a hook rather than a field, for `irTailObserved`'s reason. It costs
// one nil check per projection.
//
//	`k`     a builder-side value at every site: `r.k` off the owner's
//	        `projGo`, `f.k` off the `*fieldDef`, `want` off the switch on the
//	        conversion's type name. `ir.Proj.Shape` is filled from the same
//	        values, so comparing the two catches a producer that records one
//	        shape and builds another.
//	`site`  a literal, and the only possible source: which builder
//	        constructed a node is not a fact any node holds.
//	`p`     the node, so a reader takes `Kind()`, `Subject()`, `Name()`,
//	        `Index()`, `Sym()` and `Shape()` off the graph rather than off
//	        this call.
var irProjKindObserved func(site string, p *ir.Proj, k kind)
