package rt

import "strings"

// SeqJoin is `String.join`: the source's strings in push order, separator
// between each element and nowhere else. An empty source joins to "". The
// source is driven once through its `each_while`, so a lazy adapter chain
// fuses into the one walk and no intermediate List is built.
//
// ok is false when an element is not a string, which a checked program never
// produces; the VM reports it.
func SeqJoin[T any](fr *Frame, src Seq[T], separator string) (text string, ok bool) {
	var b strings.Builder
	first := true
	ok = true
	src.Run(fr, func(_ *Frame, item T) bool {
		s, isString := any(item).(string)
		if !isString {
			ok = false
			return false
		}
		if !first {
			b.WriteString(separator)
		}
		first = false
		b.WriteString(s)
		return true
	})
	return b.String(), ok
}
