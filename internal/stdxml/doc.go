// Package stdxml is std/xml/host: reading XML tokens from a Reader, or
// from text held whole, with Go's encoding/xml, and the escaping and name
// checks std/xml's writer uses. std/xml is Nomi, lowered with each
// program, over it.
//
// One reader reads every input: an xml.Decoder over a lineinput.Input, as
// internal/stdcsv's does. Over the program's Reader it is a handle
// (rt.XmlTokens): Next reads a token, Current gives its parts, and each
// crossing sets the input's frame before it reads. Over text held whole
// it is Decode, which `xml.decode` and `decode_bytes` call: it reads every
// token in one crossing and answers each one's kind and parts as Current
// gives them, reads no Reader and makes no handle, so its row is Pure.
// std/xml builds a token from its kind and parts in one place, and a tree
// from a list of tokens in one place, whichever reader read them. A
// leading byte order mark is dropped and columns are in code points. It
// reads raw tokens and does the namespace work itself, so a name keeps the
// prefix the document wrote beside the namespace it stands for, and it
// checks what the raw tokens leave out: that each end tag closes the
// element open, that a document has one root and no text outside it, that
// every prefix is declared, that no attribute or namespace declaration
// appears twice, and that an XML declaration comes first.
//
// Go's decoder fetches no external entity and expands no entity a DTD
// declares: in strict mode a reference to any entity but the five XML
// predefines and character references is an error. The reader also
// refuses a DOCTYPE unless `allow_doctype` asks to skip it, so an XXE
// document fails before its first entity.
//
// The writer is Nomi: it tracks the bindings in scope and declares a
// namespace an element's or attribute's name is in where it is not bound
// (an unprefixed attribute in a namespace gets the first free `nsN`). It
// escapes text and attribute values here (EscapeText, EscapeAttribute,
// `escape_text` and `escape_attribute`, pure crossings), replacing a
// character XML 1.0 cannot hold with U+FFFD.
package stdxml
