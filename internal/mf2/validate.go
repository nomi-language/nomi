package mf2

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind is what a message's parameter holds, as far as formatting is
// concerned.
type Kind int

const (
	// KindOther is any other type; it formats only through Localize.
	KindOther Kind = iota
	KindInt
	KindFloat
	KindDecimal
	KindString
	// KindEnum is an enum whose variants carry nothing.
	KindEnum
	// KindMessage is another message.
	KindMessage
	// KindNoun is a `Noun`: a `noun` declaration, which `:nomi:noun`
	// writes and `:nomi:gender` selects by.
	KindNoun
	// KindList is a `List<String>`, which `:nomi:list` joins and selects by
	// its length.
	KindList
	// KindDate is a calendar `Date`, which `:date` and `:datetime` write and
	// `:nomi:weekday` selects by.
	KindDate
	// KindTime is a calendar `Time`, which `:time` and `:datetime` write.
	KindTime
	// KindDateTime is a calendar `NaiveDateTime` or `DateTime`, a date and
	// a time of day, which `:date`, `:time` and `:datetime` write.
	KindDateTime
	// KindMoney is std/money's `Money`, which `:currency` writes.
	KindMoney
)

// calendarKind reports whether k is one of the calendar kinds the date
// functions write.
func calendarKind(k Kind) bool { return k == KindDate || k == KindTime || k == KindDateTime }

// Param is one parameter of a message's signature.
type Param struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind,omitempty"`
	// Type is the parameter's type as a reader writes it (`Status`), for
	// diagnostics.
	Type string `json:"type,omitempty"`
	// Variants are a KindEnum's variant names, in declaration order.
	Variants []string `json:"variants,omitempty"`
	// Localize reports whether the type implements Localize, which an
	// unannotated placeholder needs.
	Localize bool `json:"localize,omitempty"`
}

// Signature is what Validate checks a message's text against: the
// message's name and its parameters.
type Signature struct {
	Name   string
	Params []Param
}

func (s Signature) param(name string) (Param, bool) {
	for _, p := range s.Params {
		if p.Name == name {
			return p, true
		}
	}
	return Param{}, false
}

// Rules are a locale's plural rules of one kind: the category the rules
// select for a number, and the categories they can select.
type Rules interface {
	// Category is the category of the number written as text, a decimal
	// whose fraction digits are the visible ones.
	CategoryOf(number string) string
	CategoryNames() []string
}

// Locale is the locale a text is in, as Validate needs it.
type Locale struct {
	// Tag is its BCP 47 tag (`pl`), which starts each diagnostic.
	Tag string
	// Language is its language's English name (`Polish`).
	Language string
	// Cardinal and Ordinal are its plural rules.
	Cardinal, Ordinal Rules
	// Source reports that the text is the declaration's own, the source
	// locale's: it must use every parameter.
	Source bool
	// Genders and Cases are the grammatical genders and cases CLDR lists
	// for the nouns of the locale's language; none for a language that
	// has none. `nominative` is a case in every language: it is the form a
	// noun is written in when a text names no case.
	Genders, Cases []string
	// NounGenders are the genders the program's nouns have in this locale,
	// which a `:nomi:gender` selector needs an arm for (Selectors); nil
	// when they are not known, as in Validate, which checks only that each
	// key is a gender of the language.
	NounGenders []string
}

// Nominative is the case a noun is written in when a text names none.
const Nominative = "nominative"

// CaseNames are the cases a text in loc may ask a noun for: CLDR's, or
// only the nominative for a language CLDR lists no cases for.
func (loc Locale) CaseNames() []string {
	if len(loc.Cases) == 0 {
		return []string{Nominative}
	}
	return loc.Cases
}

// Weekdays are `:nomi:weekday`'s keys, Monday first.
var Weekdays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

// The functions Validate knows.
var (
	formatFunctions = map[string]bool{
		"string": true, "number": true, "integer": true, "percent": true, "currency": true,
		"date": true, "time": true, "datetime": true,
		"nomi:noun": true, "nomi:gender": true, "nomi:list": true, "nomi:weekday": true,
	}
	laterFunctions = map[string]string{
		"offset": "`:offset` is not supported yet",
		"unit":   "`:unit` is not supported yet",
	}
	// numberOptions are the options `:number` and `:percent` take, and
	// integerOptions those of `:integer`, nounOptions those of
	// `:nomi:noun`, currencyOptions those of `:currency`, styleOptions
	// those of `:date` and `:time`, and DateTimeOptions those of
	// `:datetime`, with the values each takes.
	numberOptions   = map[string]bool{"select": true, "useGrouping": true, "minimumIntegerDigits": true, "minimumFractionDigits": true, "maximumFractionDigits": true}
	integerOptions  = map[string]bool{"select": true, "useGrouping": true, "minimumIntegerDigits": true}
	nounOptions     = map[string]bool{"case": true, "count": true}
	currencyOptions = map[string]bool{"currencyDisplay": true}
	styleOptions    = map[string]bool{"style": true}
	// DateTimeOptions are `:datetime`'s options, each with the values it
	// takes: the two styles, or the fields a skeleton names.
	DateTimeOptions = map[string][]string{
		"dateStyle": styles, "timeStyle": styles,
		"weekday": {"long", "short", "narrow"}, "era": {"long", "short", "narrow"},
		"year": {"numeric", "2-digit"}, "month": {"numeric", "2-digit", "long", "short", "narrow"},
		"day": {"numeric", "2-digit"}, "hour": {"numeric", "2-digit"}, "minute": {"numeric", "2-digit"},
		"second": {"numeric", "2-digit"}, "fractionalSecondDigits": {"1", "2", "3"},
		"hourCycle": {"h11", "h12", "h23", "h24"},
	}
	styles           = []string{"full", "long", "medium", "short"}
	currencyDisplays = []string{"symbol", "narrowSymbol", "code", "name"}
	// timeFields are `:datetime`'s options that name a time of day.
	timeFields = map[string]bool{"hour": true, "minute": true, "second": true, "fractionalSecondDigits": true, "hourCycle": true, "timeStyle": true}
)

// functionNames is every function Nomi formats, for a hint.
const functionNames = "the functions are `:string`, `:number`, `:integer`, `:percent`, `:currency`, `:date`, `:time`, `:datetime`, `:nomi:noun`, `:nomi:gender`, `:nomi:list` and `:nomi:weekday`"

// binding is what a variable in a text holds: a parameter, possibly
// annotated by the declaration that named it.
type binding struct {
	param    Param
	function *Function // the annotation, nil when none
	literal  bool      // a .local of a literal
	declared bool      // named by .input or .local
	unknown  bool      // a variable already reported as unknown
}

// Validate checks m, the text of a message with signature sig in loc,
// against MF2's data model rules and Nomi's: every variable is a
// parameter or declared, each function is one Nomi formats and takes the
// parameter's type, an unannotated placeholder's type implements Localize,
// each plural selector has an arm for every category loc's rules select
// for the operand's type, an enum selector one per variant and no other,
// and the source text uses every parameter.
func Validate(m *Message, sig Signature, loc Locale) []Diagnostic {
	v := &validator{sig: sig, loc: loc, vars: map[string]binding{}, used: map[string]bool{}}
	v.run(m)
	return v.diags
}

type validator struct {
	sig   Signature
	loc   Locale
	vars  map[string]binding
	used  map[string]bool
	diags []Diagnostic
}

func (v *validator) errorf(sp Span, format string, args ...any) *Diagnostic {
	v.diags = append(v.diags, Diagnostic{Span: sp, Message: v.loc.Tag + ": " + fmt.Sprintf(format, args...)})
	return &v.diags[len(v.diags)-1]
}

func (v *validator) run(m *Message) {
	for _, d := range m.Declarations {
		v.declaration(d)
	}
	if m.Matches() {
		v.matcher(m)
	} else {
		v.pattern(m.Pattern)
	}
	if v.loc.Source {
		for _, p := range v.sig.Params {
			if !v.used[p.Name] {
				v.errorf(m.Span, "the source text of `%s` does not use `$%s`", v.sig.Name, p.Name).Hints = []string{
					"every parameter of a message appears in its source text, so a translator sees what it is for",
				}
			}
		}
	}
}

func (v *validator) declaration(d Declaration) {
	if prev, ok := v.vars[d.Name]; ok && prev.declared {
		v.errorf(d.NameSpan, "`$%s` is declared twice", d.Name)
		return
	}
	if d.Local {
		if _, isParam := v.sig.param(d.Name); isParam {
			v.errorf(d.NameSpan, "`.local $%s` names a parameter of `%s`; give the local another name", d.Name, v.sig.Name)
			return
		}
		if _, read := v.used["local:"+d.Name]; read {
			v.errorf(d.NameSpan, "`$%s` is read before this declaration", d.Name)
		}
		b := v.expressionBinding(&d.Value)
		b.declared = true
		v.vars[d.Name] = b
		return
	}
	// .input {$name :fn}
	p, ok := v.sig.param(d.Name)
	if !ok {
		v.unknownVariable(d.NameSpan, d.Name)
		v.vars[d.Name] = binding{declared: true, unknown: true, param: Param{Name: d.Name, Kind: KindOther}}
		return
	}
	if v.used[d.Name] {
		v.errorf(d.NameSpan, "`$%s` is read before its `.input` declaration", d.Name)
	}
	v.used[d.Name] = true
	b := binding{param: p, function: d.Value.Function, declared: true}
	if d.Value.Function != nil {
		v.function(d.Value.Function, b, d.Value.Operand)
	}
	v.attributes(d.Value.Attributes)
	v.vars[d.Name] = b
}

// expressionBinding is what a `.local` of e holds.
func (v *validator) expressionBinding(e *Expression) binding {
	var b binding
	if e.Operand == nil {
		v.errorf(e.Span, "a `.local` is declared from a value: `.local $name = {$param :function}`")
		return binding{unknown: true, param: Param{Kind: KindOther}}
	}
	if e.Operand.Variable {
		b = v.read(e.Operand.Span, e.Operand.Value)
	} else {
		b = binding{literal: true, param: Param{Kind: literalKind(e.Operand.Value)}}
	}
	if e.Function != nil {
		v.function(e.Function, b, e.Operand)
		b.function = e.Function
	}
	v.attributes(e.Attributes)
	return b
}

func literalKind(s string) Kind {
	if IsNumber(s) {
		if strings.ContainsAny(s, ".eE") {
			return KindDecimal
		}
		return KindInt
	}
	return KindString
}

// read resolves a variable a text reads.
func (v *validator) read(sp Span, name string) binding {
	if b, ok := v.vars[name]; ok {
		if b.declared {
			v.used[b.param.Name] = true
		}
		return b
	}
	p, ok := v.sig.param(name)
	if !ok {
		v.used["local:"+name] = true
		v.unknownVariable(sp, name)
		b := binding{unknown: true, param: Param{Name: name, Kind: KindOther}}
		v.vars[name] = b
		return b
	}
	v.used[name] = true
	return binding{param: p}
}

func (v *validator) unknownVariable(sp Span, name string) {
	d := v.errorf(sp, "`$%s` is not a parameter of `%s`", name, v.sig.Name)
	var names []string
	for _, p := range v.sig.Params {
		names = append(names, "`$"+p.Name+"`")
	}
	switch len(names) {
	case 0:
		d.Hints = []string{fmt.Sprintf("`%s` takes no parameters", v.sig.Name)}
	default:
		d.Hints = []string{fmt.Sprintf("`%s` takes %s", v.sig.Name, joinAnd(names))}
	}
}

func (v *validator) attributes([]Attribute) {}

// function checks f, applied to the value b holds (or to the literal
// operand), and its options.
func (v *validator) function(f *Function, b binding, operand *Operand) {
	p := b.param
	if msg, later := laterFunctions[f.Name]; later {
		v.errorf(f.Span, "%s", msg)
		return
	}
	if !formatFunctions[f.Name] {
		v.errorf(f.Span, "unknown function `:%s`", f.Name).Hints = []string{functionNames}
		return
	}
	seen := map[string]bool{}
	for _, o := range f.Options {
		if seen[o.Name] {
			v.errorf(o.NameSpan, "the option `%s` is given twice", o.Name)
			continue
		}
		seen[o.Name] = true
		v.option(f.Name, o)
	}
	if b.unknown {
		return
	}
	what := "`$" + p.Name + "`"
	if operand != nil && !operand.Variable {
		what = "the literal `" + literalText(operand.Value) + "`"
	} else if operand != nil && operand.Value != p.Name {
		what = "`$" + operand.Value + "`"
	}
	switch f.Name {
	case "integer":
		if operand != nil && !operand.Variable {
			if !IsNumber(operand.Value) {
				v.errorf(operand.Span, "`:integer` formats a number, and %s is not one", what)
			}
			return
		}
		if p.Kind != KindInt {
			v.errorf(f.Span, "`:integer` formats an `Int`, and %s is %s", what, typePhrase(p))
		}
	case "number", "percent":
		if operand != nil && !operand.Variable {
			if !IsNumber(operand.Value) {
				v.errorf(operand.Span, "`:%s` formats a number, and %s is not one", f.Name, what)
			}
			return
		}
		switch p.Kind {
		case KindInt, KindFloat, KindDecimal:
		default:
			v.errorf(f.Span, "`:%s` formats an `Int`, `Float` or `Decimal`, and %s is %s", f.Name, what, typePhrase(p))
		}
	case "string":
		if operand != nil && !operand.Variable {
			return
		}
		switch p.Kind {
		case KindString, KindEnum:
		default:
			v.errorf(f.Span, "`:string` formats a `String` or an enum whose variants carry nothing, and %s is %s", what, typePhrase(p))
		}
	case "nomi:noun", "nomi:gender":
		if p.Kind != KindNoun {
			v.errorf(f.Span, "`:%s` takes a `Noun`, and %s is %s", f.Name, what, typePhrase(p)).Hints = []string{
				"a `Noun` is the value a `noun` declaration names, passed as a parameter typed `Noun`",
			}
		}
	case "nomi:list":
		if p.Kind != KindList {
			v.errorf(f.Span, "`:nomi:list` formats a `List<String>`, and %s is %s", what, typePhrase(p))
		}
	case "nomi:weekday":
		if p.Kind != KindDate {
			v.errorf(f.Span, "`:nomi:weekday` selects by a `Date`'s day of the week, and %s is %s", what, typePhrase(p))
		}
	case "currency":
		if operand != nil && !operand.Variable || p.Kind != KindMoney {
			v.errorf(f.Span, "`:currency` formats a `Money`, and %s is %s", what, typePhrase(p)).Hints = []string{
				"a `Money` (std/money) carries its amount and its currency; pass one as the parameter",
			}
		}
	case "date", "time", "datetime":
		if operand != nil && !operand.Variable || !calendarKind(p.Kind) {
			v.errorf(f.Span, "`:%s` formats a calendar value (std/calendar's `Date`, `Time`, `NaiveDateTime` or `DateTime`), and %s is %s", f.Name, what, typePhrase(p))
			return
		}
		switch {
		case f.Name == "date" && p.Kind == KindTime:
			v.errorf(f.Span, "`:date` writes a date, and %s is a `Time`, which has none", what).Hints = []string{"write it with `:time`"}
		case f.Name == "time" && p.Kind == KindDate:
			v.errorf(f.Span, "`:time` writes a time of day, and %s is a `Date`, which has none", what).Hints = []string{"write it with `:date`"}
		case f.Name == "datetime":
			v.dateTimeFields(f, p, what)
		}
	}
}

// dateTimeFields checks that `:datetime`'s options name parts its value
// has, and either its styles or its fields.
func (v *validator) dateTimeFields(f *Function, p Param, what string) {
	styled, fields := false, false
	for _, o := range f.Options {
		if _, known := DateTimeOptions[o.Name]; !known {
			continue
		}
		if o.Name == "dateStyle" || o.Name == "timeStyle" {
			styled = true
		} else if o.Name != "hourCycle" {
			fields = true
		}
		switch {
		case p.Kind == KindDate && timeFields[o.Name]:
			v.errorf(o.NameSpan, "`%s` names a time of day, and %s is a `Date`, which has none", o.Name, what)
		case p.Kind == KindTime && !timeFields[o.Name]:
			v.errorf(o.NameSpan, "`%s` names part of a date, and %s is a `Time`, which has none", o.Name, what)
		}
	}
	if styled && fields {
		v.errorf(f.Span, "`:datetime` takes `dateStyle` and `timeStyle`, or the fields it writes (`month=long day=numeric`), not both")
	}
}

func typePhrase(p Param) string {
	if p.Type == "" {
		return "not one"
	}
	article := "a"
	if strings.ContainsRune("AEIOU", rune(p.Type[0])) {
		article = "an"
	}
	return article + " `" + p.Type + "`"
}

func (v *validator) option(fn string, o Option) {
	allowed := numberOptions
	switch fn {
	case "integer":
		allowed = integerOptions
	case "nomi:noun":
		allowed = nounOptions
	case "currency":
		allowed = currencyOptions
	case "date", "time":
		allowed = styleOptions
	case "datetime":
		allowed = map[string]bool{}
		for name := range DateTimeOptions {
			allowed[name] = true
		}
	case "string", "nomi:gender", "nomi:list", "nomi:weekday":
		allowed = nil
	}
	if !allowed[o.Name] {
		var names []string
		for n := range allowed {
			names = append(names, "`"+n+"`")
		}
		sort.Strings(names)
		d := v.errorf(o.NameSpan, "`:%s` has no option `%s`", fn, o.Name)
		if len(names) > 0 {
			d.Hints = []string{fmt.Sprintf("its options are %s", joinAnd(names))}
		}
		return
	}
	if o.Value.Variable {
		if fn == "nomi:noun" && o.Name == "count" {
			if b := v.read(o.Value.Span, o.Value.Value); !b.unknown && !b.literal && b.param.Kind != KindInt {
				v.errorf(o.Value.Span, "`count` is an `Int`, and `$%s` is %s", o.Value.Value, typePhrase(b.param))
			}
			return
		}
		v.errorf(o.Value.Span, "the option `%s` takes a literal here, not a variable", o.Name)
		return
	}
	val := o.Value.Value
	oneOf := func(values []string) {
		if !contains(values, val) {
			v.errorf(o.Value.Span, "`%s` is %s, not `%s`", o.Name, orList(quoteAll(values)), val)
		}
	}
	if values, ok := DateTimeOptions[o.Name]; ok && fn == "datetime" {
		oneOf(values)
		return
	}
	switch o.Name {
	case "style":
		oneOf(styles)
		return
	case "currencyDisplay":
		oneOf(currencyDisplays)
		return
	}
	switch o.Name {
	case "case":
		if !contains(v.loc.CaseNames(), val) {
			d := v.errorf(o.Value.Span, "`%s` is not a case in %s", val, v.loc.Language)
			if len(v.loc.Cases) == 0 {
				d.Hints = []string{fmt.Sprintf("%s's nouns have one form, the `nominative`, which `:nomi:noun` writes when the text names no case", v.loc.Language)}
			} else {
				d.Hints = []string{fmt.Sprintf("%s has %s", v.loc.Language, joinAnd(quoteAll(v.loc.Cases)))}
			}
		}
	case "count":
		if n, err := strconv.Atoi(val); err != nil || n < 0 {
			v.errorf(o.Value.Span, "`count` is a whole number of zero or more, or an `Int` parameter, not `%s`", val)
		}
	case "select":
		if val != "plural" && val != "ordinal" && val != "exact" {
			v.errorf(o.Value.Span, "`select` is `plural`, `ordinal` or `exact`, not `%s`", val)
		}
	case "useGrouping":
		if val != "auto" && val != "always" && val != "never" && val != "min2" {
			v.errorf(o.Value.Span, "`useGrouping` is `auto`, `always`, `never` or `min2`, not `%s`", val)
		}
	default:
		n, err := strconv.Atoi(val)
		if err != nil || n < 0 || n > 20 || o.Name == "minimumIntegerDigits" && n < 1 {
			low := 0
			if o.Name == "minimumIntegerDigits" {
				low = 1
			}
			v.errorf(o.Value.Span, "`%s` is a whole number from %d to 20, not `%s`", o.Name, low, val)
		}
	}
}

func (v *validator) pattern(p Pattern) {
	for _, part := range p.Parts {
		switch {
		case part.Expr != nil:
			v.placeholder(part.Expr)
		case part.Markup != nil:
			for _, o := range part.Markup.Options {
				if o.Value.Variable {
					v.read(o.Value.Span, o.Value.Value)
				}
			}
		}
	}
}

func (v *validator) placeholder(e *Expression) {
	if e.Operand == nil {
		v.errorf(e.Span, "a placeholder formats a value: write `{$name}` or `{|text|}`")
		return
	}
	if !e.Operand.Variable {
		if e.Function != nil {
			v.function(e.Function, binding{literal: true, param: Param{Kind: literalKind(e.Operand.Value)}}, e.Operand)
		}
		return
	}
	b := v.read(e.Operand.Span, e.Operand.Value)
	for _, o := range optionsOf(e.Function) {
		if o.Value.Variable {
			v.read(o.Value.Span, o.Value.Value)
		}
	}
	if e.Function != nil {
		v.function(e.Function, b, e.Operand)
		if e.Function.Name == "nomi:weekday" {
			v.weekdayPlaceholder(e)
		}
		return
	}
	if b.function != nil && b.function.Name == "nomi:weekday" {
		v.weekdayPlaceholder(e)
		return
	}
	if b.function != nil || b.literal || b.unknown || b.param.Localize {
		return
	}
	v.errorf(e.Span, "`$%s` is %s, which does not implement `Localize`", b.param.Name, typePhrase(b.param)).Hints = []string{
		fmt.Sprintf("a placeholder with no function formats its value with `Localize`: implement `Localize for %s`, or annotate it, as `{$%s :string}` for an enum", b.param.Type, b.param.Name),
	}
}

// weekdayPlaceholder reports a placeholder that writes a `:nomi:weekday`
// value: the function selects, and writes nothing.
func (v *validator) weekdayPlaceholder(e *Expression) {
	v.errorf(e.Span, "`:nomi:weekday` selects by the day of the week and writes nothing").Hints = []string{
		"select with it, `.input {$date :nomi:weekday}` then `.match $date`, and write each day's name in its arm",
	}
}

func optionsOf(f *Function) []Option {
	if f == nil {
		return nil
	}
	return f.Options
}

func (v *validator) matcher(m *Message) {
	type selector struct {
		b    binding
		name string
		sp   Span
	}
	sels := make([]selector, len(m.Selectors))
	for i, s := range m.Selectors {
		b := v.read(s.Span, s.Name)
		sels[i] = selector{b: b, name: s.Name, sp: s.Span}
		if b.function == nil && !b.unknown {
			d := v.errorf(s.Span, "the selector `$%s` has no function to select by", s.Name)
			d.Hints = []string{fmt.Sprintf("declare it with one first, as `.input {$%s :integer}` or `.input {$%s :string}`", s.Name, s.Name)}
		}
	}
	// MF2's data model rules: as many keys as selectors, no two variants
	// with the same keys, and a variant of only `*`.
	seen := map[string]bool{}
	fallback := false
	for _, vr := range m.Variants {
		if len(vr.Keys) != len(sels) {
			v.errorf(vr.Span, "this variant has %d %s and the `.match` %d %s", len(vr.Keys), plural(len(vr.Keys), "key", "keys"), len(sels), plural(len(sels), "selector", "selectors"))
			continue
		}
		var id []string
		for _, k := range vr.Keys {
			if k.CatchAll {
				id = append(id, "*")
			} else {
				id = append(id, "="+k.Value)
			}
		}
		key := strings.Join(id, " ")
		if seen[key] {
			v.errorf(vr.Span, "two variants have the keys `%s`", strings.ReplaceAll(key, "=", ""))
		}
		seen[key] = true
		fallback = fallback || vr.CatchAll()
		v.pattern(vr.Pattern)
	}
	if !fallback {
		stars := strings.TrimSpace(strings.Repeat("* ", len(sels)))
		v.errorf(m.Span, "`%s` has no variant whose keys are all `*`", v.sig.Name).Hints = []string{
			fmt.Sprintf("MessageFormat 2 requires one, even where the other arms cover every value: add `%s {{...}}`", stars),
		}
	}
	for i, s := range sels {
		if s.b.function == nil {
			continue
		}
		var keys []Key
		for _, vr := range m.Variants {
			if len(vr.Keys) == len(sels) && !vr.Keys[i].CatchAll {
				keys = append(keys, vr.Keys[i])
			}
		}
		v.selectorKeys(s.name, s.b, keys, s.sp)
	}
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}

// selectorKeys checks the keys a selector's position holds.
func (v *validator) selectorKeys(name string, b binding, keys []Key, whole Span) {
	f := b.function
	switch f.Name {
	case "integer", "number", "percent", "nomi:list":
		if f.Name == "nomi:list" && b.param.Kind != KindList {
			return
		}
		mode := "plural"
		if o, ok := f.Option("select"); ok && !o.Value.Variable {
			mode = o.Value.Value
		}
		for _, k := range keys {
			if IsNumber(k.Value) {
				continue
			}
			if mode == "exact" {
				v.errorf(k.Span, "`%s` is not a number, and `select=exact` matches numbers only", k.Value)
			}
		}
		if mode == "exact" {
			return
		}
		if b.param.Kind == KindFloat && f.Name != "integer" {
			v.errorf(f.Span, "a `Float` cannot select a plural category: its category depends on the digits it shows, which a `Float` does not keep").Hints = []string{
				fmt.Sprintf("make `$%s` a `Decimal`, or format it with `:integer`", b.param.Name),
			}
			return
		}
		rules, needed, kind, ok := pluralKeys(f, b.param, v.loc)
		if !ok {
			return
		}
		integers := f.Name == "integer" || f.Name == "nomi:list" || b.param.Kind == KindInt
		known := map[string]bool{}
		for _, c := range rules.CategoryNames() {
			known[c] = true
		}
		has := map[string]bool{}
		for _, k := range keys {
			if IsNumber(k.Value) {
				continue
			}
			if !known[k.Value] {
				all := rules.CategoryNames()
				quoted := make([]string, len(all))
				for i, c := range all {
					quoted[i] = "`" + c + "`"
				}
				v.errorf(k.Span, "`%s` is not a %s category in %s", k.Value, kind, v.loc.Language).Hints = []string{
					fmt.Sprintf("%s has %s", v.loc.Language, joinAnd(quoted)),
				}
				continue
			}
			has[k.Value] = true
		}
		for _, c := range needed {
			if has[c] {
				continue
			}
			ex := examples(rules, c, integers)
			verb := "selects"
			if len(ex) > 1 {
				verb = "select"
			}
			v.errorf(whole, "`%s` has no `%s` arm; %s %s `%s` in %s", v.sig.Name, c, joinAnd(ex), verb, c, v.loc.Language)
		}
	case "string":
		if b.param.Kind != KindEnum {
			return
		}
		variants := map[string]bool{}
		for _, n := range b.param.Variants {
			variants[n] = true
		}
		has := map[string]bool{}
		for _, k := range keys {
			if !variants[k.Value] {
				v.errorf(k.Span, "`%s` is not a variant of `%s`", k.Value, b.param.Type).Hints = []string{
					fmt.Sprintf("`%s` has %s", b.param.Type, joinAnd(quoteAll(b.param.Variants))),
				}
				continue
			}
			has[k.Value] = true
		}
		for _, n := range b.param.Variants {
			if !has[n] {
				v.errorf(whole, "`%s` has no `%s` arm; `.match $%s` needs an arm for every variant of `%s`", v.sig.Name, n, name, b.param.Type)
			}
		}
	case "nomi:noun":
		v.errorf(whole, "`:nomi:noun` writes a noun and selects nothing").Hints = []string{
			fmt.Sprintf("select on the noun's gender with `.input {$%s :nomi:gender}`", name),
		}
	case "currency":
		v.errorf(whole, "`:currency` writes an amount and selects nothing").Hints = []string{
			"select on a count instead, as `.input {$count :integer}`",
		}
	case "date", "time", "datetime":
		v.errorf(whole, "`:%s` writes a calendar value and selects nothing", f.Name).Hints = []string{
			fmt.Sprintf("select by the day of the week with `.input {$%s :nomi:weekday}`", name),
		}
	case "nomi:gender":
		if b.param.Kind != KindNoun {
			return
		}
		if len(v.loc.Genders) == 0 {
			v.errorf(whole, "%s nouns have no grammatical gender, so `%s` cannot select on `$%s`'s", v.loc.Language, v.sig.Name, name).Hints = []string{
				"select on a gender only in a translation into a language whose nouns have one",
			}
			return
		}
		for _, k := range keys {
			if !contains(v.loc.Genders, k.Value) {
				v.errorf(k.Span, "`%s` is not a gender in %s", k.Value, v.loc.Language).Hints = []string{
					fmt.Sprintf("%s has %s", v.loc.Language, joinAnd(quoteAll(v.loc.Genders))),
				}
			}
		}
	case "nomi:weekday":
		if b.param.Kind != KindDate {
			return
		}
		has := map[string]bool{}
		for _, k := range keys {
			if !contains(Weekdays, k.Value) {
				v.errorf(k.Span, "`%s` is not a day of the week", k.Value).Hints = []string{
					fmt.Sprintf("the days are %s", joinAnd(quoteAll(Weekdays))),
				}
				continue
			}
			has[k.Value] = true
		}
		if len(has) == 0 {
			return
		}
		for _, d := range Weekdays {
			if !has[d] {
				v.errorf(whole, "`%s` has no `%s` arm; a `.match` on `$%s`'s day of the week that names one day needs all seven", v.sig.Name, d, name)
			}
		}
	}
}

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return out
}

// orList joins items as English does with `or`: `a or b`, `a, b or c`.
func orList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// joinAnd joins items as English does: `a`, `a and b`, `a, b and c`.
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// integerSamples are the whole numbers integerCategories tries: every
// number to 1000, and the powers and multiples of a million some
// languages' rules single out.
var integerSamples = func() []string {
	var out []string
	for i := 0; i <= 1000; i++ {
		out = append(out, strconv.Itoa(i))
	}
	for _, n := range []string{"10000", "100000", "1000000", "2000000", "10000000", "100000000", "1000000000"} {
		out = append(out, n)
	}
	return out
}()

// decimalSamples are the fractional numbers examples tries.
var decimalSamples = []string{"0.0", "0.1", "0.5", "1.0", "1.5", "2.5", "0.01", "1.25", "2.0", "5.0", "10.0", "100.0", "1000000.0"}

// integerCategories are the categories rules select for some whole number.
func integerCategories(r Rules) []string {
	found := map[string]bool{}
	for _, n := range integerSamples {
		found[r.CategoryOf(n)] = true
	}
	var out []string
	for _, c := range r.CategoryNames() {
		if found[c] {
			out = append(out, c)
		}
	}
	return out
}

// examples are up to three numbers rules select category for: whole
// numbers first, and fractional ones when whole numbers are not all the
// operand may be.
func examples(r Rules, category string, integers bool) []string {
	var out []string
	for _, n := range integerSamples {
		if r.CategoryOf(n) == category {
			out = append(out, n)
			if len(out) == 3 {
				return out
			}
		}
	}
	if !integers {
		for _, n := range decimalSamples {
			if r.CategoryOf(n) == category {
				out = append(out, n)
				if len(out) == 3 {
					break
				}
			}
		}
	}
	return out
}

// Use is how a text uses one variable: the functions it formats or selects
// it with, and whether a placeholder writes it with none.
type Use struct {
	Functions map[string]bool
	Bare      bool
}

// Uses are how m uses each variable that is not one of its own `.local`s,
// by name: through a `.local` declared from it, an `.input`, a placeholder
// or an option.
func Uses(m *Message) map[string]*Use {
	out := map[string]*Use{}
	locals := map[string]string{} // a local -> the variable it is declared from
	annotated := map[string]bool{}
	use := func(name string) *Use {
		for {
			src, ok := locals[name]
			if !ok {
				break
			}
			name = src
		}
		u := out[name]
		if u == nil {
			u = &Use{Functions: map[string]bool{}}
			out[name] = u
		}
		return u
	}
	for _, d := range m.Declarations {
		e := d.Value
		if e.Operand != nil && e.Operand.Variable {
			u := use(e.Operand.Value)
			if e.Function != nil {
				u.Functions[e.Function.Name] = true
			}
		}
		if d.Local {
			if e.Operand != nil && e.Operand.Variable {
				locals[d.Name] = e.Operand.Value
			}
		}
		if e.Function != nil {
			annotated[d.Name] = true
		}
	}
	var pattern func(p Pattern)
	pattern = func(p Pattern) {
		for _, part := range p.Parts {
			e := part.Expr
			if e == nil || e.Operand == nil || !e.Operand.Variable {
				continue
			}
			u := use(e.Operand.Value)
			switch {
			case e.Function != nil:
				u.Functions[e.Function.Name] = true
			case !annotated[e.Operand.Value]:
				u.Bare = true
			}
		}
	}
	pattern(m.Pattern)
	for _, s := range m.Selectors {
		use(s.Name)
	}
	for _, v := range m.Variants {
		pattern(v.Pattern)
	}
	for name := range locals {
		delete(out, name)
	}
	return out
}
