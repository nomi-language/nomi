package analysis

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ast"
)

// An impl the compiler provides for building a value. A validated type,
// and a `type` over a subtype, implements `TryFrom` of its base through
// the `try_from` its rule supplies (subtype_synthesis.go, tryFromImpl).
// That function builds a value of the type, so for an opaque type it is
// construction, private to the defining file (opaqueOutside), as its call
// form is. Outside it the impl does not
// count toward a bound (spec §4 *Opaque distinct types*).
//
// The checker decides it where it decides that a type implements an
// interface at a site, by asking implementsOnlyByConstruction: at a bound
// a generic call, an explicit type argument or a generic function value
// solves (typeImplementsInterface, whose error names the bound), and at
// every demand it records of a concrete type (recordConformanceRecording):
// a value converted to an interface, the bound of an impl a use selects.
// Among the type arguments a bound compares, the impl is left out
// (implArgLists). A call that names the provided function itself is
// refused as construction (refuseProvidedConversionCalls). A `try_from`
// the type's file writes in an `impl TryFrom<E>` of its own is public and
// counts everywhere.
//
// The resolvers the IR builder shares (implBlockFor, implReceiverMatches)
// do not ask: a generic body in another file may be instantiated at the
// type by a call the defining file makes, which this rule admits, and the
// builder lowers that body's `T.try_from` through the provided impl.

// constructionImpl reports whether ib is an impl block the compiler
// provides for building its receiver: one whose functions are provided
// conversions (providedConversion).
func constructionImpl(ib *ast.ImplBlock) bool {
	if ib == nil || ib.Interface == nil || len(ib.Items) == 0 {
		return false
	}
	for _, item := range ib.Items {
		if providedConversion(item) == nil {
			return false
		}
	}
	return true
}

// hidesConstruction reports whether info, an impl recv has, is one the
// compiler provides for building recv, and recv is an opaque type this file
// may not build: an impl that counts toward no bound here.
func (c *checker) hidesConstruction(info *ImplTypeArgs, recv Type) bool {
	if info == nil || !constructionImpl(info.Block) {
		return false
	}
	name, _ := c.opaqueOutside(recv)
	return name != ""
}

// implementsOnlyByConstruction reports whether recv has impls of the
// interface iface (declared by origin) and each is one this file may not
// count (hidesConstruction), so recv does not implement iface here.
func (c *checker) implementsOnlyByConstruction(recv Type, iface, origin string) bool {
	if c == nil || c.fa == nil {
		return false
	}
	if name, _ := c.opaqueOutside(recv); name == "" {
		return false
	}
	blocks := c.fa.implBlocksOf(recv, iface, origin)
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if !c.hidesConstruction(b, recv) {
			return false
		}
	}
	return true
}

// withConstructionReason is e, an error that concrete does not implement
// iface here, with the reason and the opaque-construction code when only
// the impls the compiler provides for building concrete would meet it
// (implementsOnlyByConstruction), and false for any other concrete.
func (c *checker) withConstructionReason(e TypeError, concrete Type, iface *InterfaceType) (TypeError, bool) {
	name, home := c.opaqueOutside(concrete)
	if name == "" || c.fa == nil {
		return e, false
	}
	for _, b := range c.fa.implBlocksOf(concrete, iface.Name, iface.Origin) {
		if c.hidesConstruction(b, concrete) {
			help := fmt.Sprintf("'%s' is opaque, and its construction is private to %s: the `try_from` its rule provides builds a value, so its `%s` impl counts only there; use an exported constructor function", name, home, iface.Name)
			return e.WithHint(help).coded(OpaqueConstructionCode, name), true
		}
	}
	return e, false
}

// constructionDemandError reports the demand rec makes of concrete, an
// implementation of ifaceName that only the impls the compiler provides
// for building concrete would meet (implementsOnlyByConstruction), as a
// missing impl is reported (missingImplError), with the reason.
func (c *checker) constructionDemandError(concrete Type, ifaceName string, rec Recording) {
	msg := fmt.Sprintf("no impl of `%s` for `%s` (%s)", ifaceName, concrete, requiredClause(rec))
	if c.alreadyReported(rec.Pos.Line, rec.Pos.Col, msg) {
		return
	}
	e, _ := c.withConstructionReason(TypeError{Line: rec.Pos.Line, Col: rec.Pos.Col, Message: msg}, concrete, &InterfaceType{Name: ifaceName, Origin: rec.IfaceOrigin})
	c.report(e)
}
