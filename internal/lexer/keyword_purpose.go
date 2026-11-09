package lexer

// keywordPurposes says, for each reserved word, what it is for in a few
// words and a name to write instead of it, which the parser's error at a
// reserved word written as a name gives. TestEveryReservedWordHasAPurpose
// holds it to the keywords map.
var keywordPurposes = map[string][2]string{
	"and":         {"logical and", "both"},
	"as":          {"renames an import or names a whole pattern match", "alias"},
	"assert":      {"a test's assertion", "check"},
	"break":       {"stops an iteration", "stop"},
	"case":        {"matches a value against patterns", "kind"},
	"continue":    {"goes on to the next iteration", "next"},
	"dbg":         {"prints a value while debugging", "debug"},
	"defer":       {"runs a call when its block ends", "later"},
	"else":        {"the other branch of an `if`", "otherwise"},
	"enum":        {"declares an enum", "variant"},
	"export":      {"exports a function to the host", "exported"},
	"extern":      {"reserved, with no construct", "external"},
	"fn":          {"declares a function or a lambda", "f"},
	"for":         {"names the type an `impl` is for", "target"},
	"if":          {"a conditional", "condition"},
	"impl":        {"implements an interface or a type's functions", "implementation"},
	"import":      {"imports a file", "imported"},
	"in":          {"tests membership, `x in collection`", "input"},
	"interface":   {"declares an interface", "contract"},
	"is":          {"tests a value against a pattern", "matches"},
	"not":         {"logical not", "negated"},
	"once":        {"declares a value computed on first read", "cached"},
	"opaque":      {"hides a type's representation", "hidden"},
	"or":          {"logical or", "either"},
	"pub":         {"makes a declaration public", "public"},
	"refute":      {"a test's negative assertion", "check"},
	"return":      {"leaves a function with a value", "result"},
	"self":        {"the implementing type in an interface", "value"},
	"struct":      {"declares a struct", "record"},
	"tap":         {"a pipe stage run for its effect", "peek"},
	"test":        {"declares a test", "check"},
	"then":        {"a pipe stage that applies a function", "next"},
	"type":        {"declares a distinct type", "kind"},
	"typealias":   {"declares another name for a type", "alias"},
	"unreachable": {"marks code that cannot run", "never"},
	"when":        {"an arm's guard", "condition"},
	"where":       {"bounds type parameters", "place"},
	"with":        {"replaces an application field for a block", "options"},
}

// KeywordPurpose is what the reserved word word is for and a name to
// write instead of it, or false when word is not reserved.
func KeywordPurpose(word string) (purpose, instead string, ok bool) {
	p, ok := keywordPurposes[word]
	return p[0], p[1], ok
}
