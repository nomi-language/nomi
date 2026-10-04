package analysis

// Nominal type identity.
//
// A struct, enum, distinct type, interface, or non-generic host type is
// identified by the pair (Origin, Name) — never by Name alone. Origin is the
// declaring file's build key, the same key BuildProject files itself by:
//
//	OriginEntry     the project entry file
//	"std/calendar"  a stdlib file
//	"shapes"        a sibling file in the entry's module
//	"stringkit/pad" a file in a cross-module dependency
//
// Keys are unique per build, so identity needs no disambiguation pass.
//
// Origin is a separate field rather than a prefix on Name. Prefixing
// forces every comparison to strip the prefix back off. Keeping Name
// short means every display, hover, and diagnostic consumer reads it
// unchanged.

// OriginEntry is the Origin of types declared in the project entry file.
// The entry's build key is "" — the empty string is reserved for
// OriginUnresolved, so the entry gets an explicit token instead. The
// angle brackets cannot collide with a real key: import path segments
// are restricted to [_A-Za-z][_A-Za-z0-9]* (parser.isImportPathSegment).
const OriginEntry = "<entry>"

// OriginUnresolved is the Origin of a type value that did not come from
// a declaration — a shape placeholder the checker fabricates for a
// stdlib type when the real declaration is not loaded (single-file
// analysis with no stdlib). It is the bottom of the identity lattice:
// it matches any origin with the same name, because in a build where no
// real declaration was available there is nothing for it to be confused
// with. Declarations never carry it.
const OriginUnresolved = ""

// sameNominalIdentity reports whether two nominal type names refer to
// the same declaration. Names must match; origins must match unless
// either side is unresolved.
func sameNominalIdentity(originA, nameA, originB, nameB string) bool {
	if nameA != nameB {
		return false
	}
	if originA == OriginUnresolved || originB == OriginUnresolved {
		return true
	}
	return originA == originB
}

// originForKey maps a build file key to the Origin stamped on the types
// that file declares.
func originForKey(key string) string {
	if key == "" {
		return OriginEntry
	}
	return key
}

// originForStandalonePath is the Origin for the single-file analysis
// paths (`BuildFileWithStdlibAtPath`), which have no build key — the LSP
// analyzing an open document, and the runtime analyzing one module file.
//
// A stdlib file must resolve to its real key even when it is the file
// under analysis. Opening std/calendar.nomi in the editor analyzes it as
// the document AND loads it as an import of itself; if the document's
// declarations carried OriginEntry, its own `Date` would not be the
// `Date` its imports resolve to, and every signature in the file would
// report `expected Date, got Date`.
//
// Stdlib-ness, and which module the file is, is stdlibModuleForPath's question
// — the same one DocumentManager.isStdlibFile and isStdlibRecording ask.
func originForStandalonePath(filePath string) string {
	name, ok := stdlibModuleForPath(filePath)
	if !ok {
		return OriginEntry
	}
	return "std/" + name
}

// nominalOrigin reports a type's declaring-module key, and whether the type is
// nominal at all. An interface is included: an existential parameter position
// takes one, and two same-named interfaces are two interfaces. A non-generic
// host type is nominal; a built-in primitive singleton is not.
func nominalOrigin(t Type) (string, bool) {
	switch v := t.(type) {
	case *PrimitiveType:
		if v.Origin != "" {
			return v.Origin, true
		}
	case *StructType:
		return v.Origin, true
	case *EnumType:
		return v.Origin, true
	case *DistinctType:
		return v.Origin, true
	case *InterfaceType:
		return v.Origin, true
	}
	return "", false
}
