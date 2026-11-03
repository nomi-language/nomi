package htmltemplate

import "strings"

// The element names a template may write: HTML's, SVG's and MathML's, and
// any name with a hyphen, which HTML reserves for custom elements. A name
// outside these is almost always a typo (`<dvi>`).

var htmlElements = setOf(`a abbr address area article aside audio b base bdi bdo blockquote body br
button canvas caption cite code col colgroup data datalist dd del details dfn dialog div dl dt em
embed fieldset figcaption figure footer form h1 h2 h3 h4 h5 h6 head header hgroup hr html i iframe
img input ins kbd label legend li link main map mark menu meta meter nav noscript object ol optgroup
option output p picture pre progress q rp rt ruby s samp script search section select slot small
source span strong style sub summary sup table tbody td template textarea tfoot th thead time title
tr track u ul var video wbr`)

// voidElements have no content and no end tag (HTML's void elements).
var voidElements = setOf(`area base br col embed hr img input link meta source track wbr`)

// svgElements are SVG's element names, lowercased: HTML parsing is
// case-insensitive, so `<lineargradient>` and `<linearGradient>` are one
// element.
var svgElements = setOf(strings.ToLower(`svg a animate animateMotion animateTransform circle clipPath
defs desc ellipse feBlend feColorMatrix feComponentTransfer feComposite feConvolveMatrix
feDiffuseLighting feDisplacementMap feDistantLight feDropShadow feFlood feFuncA feFuncB feFuncG
feFuncR feGaussianBlur feImage feMerge feMergeNode feMorphology feOffset fePointLight
feSpecularLighting feSpotLight feTile feTurbulence filter foreignObject g image line linearGradient
marker mask metadata mpath path pattern polygon polyline radialGradient rect script set stop style
switch symbol text textPath title tspan use view`))

var mathmlElements = setOf(`math maction annotation annotation-xml menclose merror mfenced mfrac mi
mmultiscripts mn mo mover mpadded mphantom mprescripts mroot mrow ms mspace msqrt mstyle msub
msubsup msup mtable mtd mtext mtr munder munderover none semantics`)

// urlAttributes are the attributes whose value HTML defines as a URL: the
// list Go's html/template uses. `data` is a URL only on `<object>`.
var urlAttributes = setOf(`action background cite formaction href icon longdesc manifest poster
profile src usemap xmlns`)

func setOf(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// knownElement reports whether name (lowercased) is an element a template
// may write.
func knownElement(name string) bool {
	return htmlElements[name] || svgElements[name] || mathmlElements[name] || strings.Contains(name, "-")
}

// isURLAttribute reports whether attribute attr of element elem holds a
// URL. A namespaced name (`xlink:href`) is classified by its local part,
// and `xmlns:` declarations are URLs.
func isURLAttribute(elem, attr string) bool {
	if strings.HasPrefix(attr, "xmlns:") {
		return true
	}
	if i := strings.IndexByte(attr, ':'); i >= 0 {
		attr = attr[i+1:]
	}
	return urlAttributes[attr] || (attr == "data" && elem == "object")
}

// isEventHandler reports whether attr is an event handler attribute, whose
// value is JavaScript.
func isEventHandler(attr string) bool {
	return len(attr) > 2 && strings.HasPrefix(attr, "on")
}
