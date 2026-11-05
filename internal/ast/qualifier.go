package ast

// QualifierIdent is the qualifier of a member access as a name: n itself
// for an *Ident, and for a *FilePath the name the directory import binds
// the file it reaches under, its spelling (`db::posts`), at the path's
// position. Passes that resolve a qualifier through the scope by name and
// position read a path through it as they read a short name. ok is false
// for any other node.
func QualifierIdent(n Node) (*Ident, bool) {
	switch q := n.(type) {
	case *Ident:
		return q, true
	case *FilePath:
		return &Ident{Name: q.Spelling(), Line: q.Line, Col: q.Col}, true
	}
	return nil, false
}
