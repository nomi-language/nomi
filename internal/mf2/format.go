package mf2

import (
	"sort"
	"strconv"
	"strings"
)

// Value is one argument of a message as Format reads it.
type Value struct {
	Kind Kind
	// Number is an Int's, Float's or Decimal's value as decimal text
	// (`-12.50`); a Decimal's keeps its scale.
	Number string
	// Key is a String's text or an enum variant's name: what `:string`
	// formats and selects by; a calendar value's ISO 8601 text
	// (`2026-10-20`, `09:30:00`, `2026-10-20T09:30:00+02:00[Europe/Warsaw]`),
	// which the date functions write and `:nomi:weekday` selects by; a
	// Money's currency as its literal writes it (`EUR`, `BTC:8`), whose
	// amount is Number.
	Key string
	// Text is the value as `Localize` writes it in the locale, which an
	// unannotated placeholder formats to: for a list the items joined as
	// the locale joins them, whose count is Number, and for a noun its
	// table (EncodeNoun).
	Text string
}

// NumberFormat is how Env.Number writes a number.
type NumberFormat struct {
	// Percent writes the number, already times 100, with the locale's
	// percent pattern.
	Percent bool
	// Grouping is `useGrouping`: auto, always, never or min2.
	Grouping string
	// MinInteger pads the integer digits; MinFraction and MaxFraction bound
	// the fraction digits, MaxFraction -1 keeping every digit the number has.
	MinInteger, MinFraction, MaxFraction int
}

// Env is what Format reads of a locale.
type Env interface {
	// Plural is the category the locale's plural rules (ordinal ones when
	// ordinal) select for the decimal text number.
	Plural(number string, ordinal bool) string
	// Number writes the decimal text number as the locale writes it.
	Number(number string, f NumberFormat) string
	// RTL reports that the locale is written right to left: each
	// placeholder is then wrapped in Unicode isolates.
	RTL() bool
	// Text is a pattern's literal text as the locale shows it: itself, or a
	// pseudo-locale's transform of it.
	Text(s string) string
	// Noun writes the noun whose table is noun (EncodeNoun) in case kase,
	// for count things (decimal text), in the locale of the message's
	// text.
	Noun(noun, kase, count string) string
	// Gender is the gender of the noun whose table is noun in the locale
	// of the message's text, "" when it has none there.
	Gender(noun string) string
	// Money writes the amount (decimal text) in the currency (its literal's
	// text), showing the currency as display says: symbol, narrowSymbol,
	// code or name.
	Money(amount, currency, display string) string
	// Date writes the calendar value v by request.
	Date(v Value, request DateRequest) string
}

// DateRequest is how a date function writes a calendar value: at a date
// style and a time style ("" for a part not written), or, when Skeleton is
// set, by the fields the skeleton names.
type DateRequest struct {
	DateStyle, TimeStyle string
	Skeleton             string
}

// DateRequestOf is how f, one of `:date`, `:time` and `:datetime`, writes a
// value of kind k: `:date` the date at its `style` (medium), `:time` the
// time at its (short), and `:datetime` its styles, its fields as a
// skeleton (Skeleton), or the date at medium and the time at short.
func DateRequestOf(f *Function, k Kind) DateRequest {
	opt := func(name string) string {
		if o, ok := f.Option(name); ok && !o.Value.Variable {
			return o.Value.Value
		}
		return ""
	}
	orDefault := func(s, def string) string {
		if s == "" {
			return def
		}
		return s
	}
	hasDate, hasTime := k != KindTime, k != KindDate
	switch f.Name {
	case "date":
		return DateRequest{DateStyle: orDefault(opt("style"), "medium")}
	case "time":
		return DateRequest{TimeStyle: orDefault(opt("style"), "short")}
	}
	if skel := Skeleton(f); skel != "" {
		return DateRequest{Skeleton: skel}
	}
	var r DateRequest
	ds, ts := opt("dateStyle"), opt("timeStyle")
	if ds == "" && ts == "" {
		ds, ts = "medium", "short"
	}
	if hasDate {
		r.DateStyle = ds
	}
	if hasTime {
		r.TimeStyle = ts
	}
	return r
}

// Skeleton is the CLDR skeleton `:datetime`'s field options name, in UTS
// #35's canonical order, or "" when it names no field: `weekday=long
// month=long day=numeric` is `MMMMEEEEd`.
func Skeleton(f *Function) string {
	letters := []struct {
		option string
		write  func(value string) string
	}{
		{"era", func(v string) string {
			return map[string]string{"long": "GGGG", "short": "G", "narrow": "GGGGG"}[v]
		}},
		{"year", func(v string) string { return map[string]string{"numeric": "y", "2-digit": "yy"}[v] }},
		{"month", func(v string) string {
			return map[string]string{"numeric": "M", "2-digit": "MM", "long": "MMMM", "short": "MMM", "narrow": "MMMMM"}[v]
		}},
		{"weekday", func(v string) string {
			return map[string]string{"long": "EEEE", "short": "EEE", "narrow": "EEEEE"}[v]
		}},
		{"day", func(v string) string { return map[string]string{"numeric": "d", "2-digit": "dd"}[v] }},
		{"hour", func(v string) string { return map[string]string{"numeric": "j", "2-digit": "jj"}[v] }},
		{"minute", func(v string) string { return map[string]string{"numeric": "m", "2-digit": "mm"}[v] }},
		{"second", func(v string) string { return map[string]string{"numeric": "s", "2-digit": "ss"}[v] }},
		{"fractionalSecondDigits", func(v string) string {
			return map[string]string{"1": "S", "2": "SS", "3": "SSS"}[v]
		}},
	}
	var b strings.Builder
	for _, l := range letters {
		if o, ok := f.Option(l.option); ok && !o.Value.Variable {
			b.WriteString(l.write(o.Value.Value))
		}
	}
	skel := b.String()
	if o, ok := f.Option("hourCycle"); ok && strings.Contains(skel, "j") {
		h := map[string]string{"h11": "K", "h12": "h", "h23": "H", "h24": "k"}[o.Value.Value]
		if h != "" {
			skel = strings.ReplaceAll(skel, "j", h)
		}
	}
	return skel
}

// moneyDisplay is `:currency`'s currencyDisplay, symbol when it names none.
func moneyDisplay(f *Function) string {
	if o, ok := f.Option("currencyDisplay"); ok && !o.Value.Variable {
		return o.Value.Value
	}
	return "symbol"
}

// Format renders m with args, by name, in env's locale. m is a message
// Validate accepted; a value it cannot format renders as MF2's fallback,
// `{$name}`.
func Format(m *Message, args map[string]Value, env Env) string {
	f := &formatter{env: env, args: args, locals: map[string]resolved{}}
	for _, d := range m.Declarations {
		if d.Local {
			f.locals[d.Name] = f.resolve(&d.Value)
		} else {
			f.locals[d.Name] = f.resolve(&d.Value)
		}
	}
	if !m.Matches() {
		return f.pattern(m.Pattern)
	}
	return f.pattern(f.choose(m))
}

// resolved is a variable's or expression's value with the function that
// annotates it.
type resolved struct {
	value    Value
	function *Function
	ok       bool
	name     string
}

type formatter struct {
	env    Env
	args   map[string]Value
	locals map[string]resolved
}

func (f *formatter) variable(name string) resolved {
	if r, ok := f.locals[name]; ok {
		return r
	}
	v, ok := f.args[name]
	return resolved{value: v, ok: ok, name: name}
}

func (f *formatter) resolve(e *Expression) resolved {
	var r resolved
	switch {
	case e.Operand == nil:
		r = resolved{ok: false}
	case e.Operand.Variable:
		r = f.variable(e.Operand.Value)
	default:
		lit := e.Operand.Value
		k := literalKind(lit)
		v := Value{Kind: k, Key: lit, Text: lit}
		if k == KindInt || k == KindDecimal {
			v.Number = plainNumber(lit)
		}
		r = resolved{value: v, ok: true, name: "|" + lit + "|"}
	}
	if e.Function != nil {
		r.function = e.Function
	}
	return r
}

// plainNumber writes an MF2 number literal without an exponent.
func plainNumber(lit string) string {
	if !strings.ContainsAny(lit, "eE") {
		return lit
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return lit
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (f *formatter) choose(m *Message) Pattern {
	matches := make([][]string, len(m.Selectors))
	for i, s := range m.Selectors {
		r := f.variable(s.Name)
		var keys []string
		for _, vr := range m.Variants {
			if i < len(vr.Keys) && !vr.Keys[i].CatchAll {
				keys = append(keys, vr.Keys[i].Value)
			}
		}
		matches[i] = f.match(r, keys)
	}
	type candidate struct {
		v    *Variant
		rank []int
	}
	var cands []candidate
	for vi := range m.Variants {
		vr := &m.Variants[vi]
		if len(vr.Keys) != len(m.Selectors) {
			continue
		}
		rank := make([]int, len(vr.Keys))
		ok := true
		for i, k := range vr.Keys {
			if k.CatchAll {
				rank[i] = len(matches[i])
				continue
			}
			pos := -1
			for j, mk := range matches[i] {
				if mk == k.Value {
					pos = j
					break
				}
			}
			if pos < 0 {
				ok = false
				break
			}
			rank[i] = pos
		}
		if ok {
			cands = append(cands, candidate{vr, rank})
		}
	}
	// MF2 sorts by the last selector first, stably, so the first selector
	// decides most.
	for i := len(m.Selectors) - 1; i >= 0; i-- {
		sort.SliceStable(cands, func(a, b int) bool { return cands[a].rank[i] < cands[b].rank[i] })
	}
	if len(cands) == 0 {
		return Pattern{}
	}
	return cands[0].v.Pattern
}

// match lists the keys that match r's value, best first.
func (f *formatter) match(r resolved, keys []string) []string {
	if !r.ok || r.function == nil {
		return nil
	}
	switch r.function.Name {
	case "string":
		for _, k := range keys {
			if k == r.value.Key {
				return []string{k}
			}
		}
		return nil
	case "nomi:gender":
		if r.value.Kind != KindNoun {
			return nil
		}
		g := f.env.Gender(r.value.Text)
		for _, k := range keys {
			if g != "" && k == g {
				return []string{k}
			}
		}
		return nil
	case "nomi:weekday":
		day := Weekday(r.value.Key)
		for _, k := range keys {
			if day != "" && k == day {
				return []string{k}
			}
		}
		return nil
	case "integer", "number", "percent", "nomi:list":
		mode := "plural"
		if o, ok := r.function.Option("select"); ok {
			mode = o.Value.Value
		}
		n := f.operand(r)
		var exact, category []string
		for _, k := range keys {
			if IsNumber(k) {
				if sameNumber(k, n) {
					exact = append(exact, k)
				}
			}
		}
		if mode != "exact" {
			cat := f.env.Plural(n, mode == "ordinal")
			for _, k := range keys {
				if k == cat {
					category = append(category, k)
				}
			}
		}
		return append(exact, category...)
	}
	return nil
}

// operand is the decimal text a numeric value selects and formats by:
// truncated to an integer by `:integer`, its fraction padded by
// `minimumFractionDigits`, times 100 by `:percent`.
func (f *formatter) operand(r resolved) string {
	n := r.value.Number
	if n == "" {
		n = "0"
	}
	switch r.function.Name {
	case "integer":
		n, _, _ = strings.Cut(n, ".")
		if n == "-0" {
			n = "0"
		}
	case "percent":
		n = shiftDecimal(n, 2)
	}
	if o, ok := r.function.Option("minimumFractionDigits"); ok {
		if want, err := strconv.Atoi(o.Value.Value); err == nil {
			ip, fp, _ := strings.Cut(n, ".")
			if len(fp) < want {
				fp += strings.Repeat("0", want-len(fp))
			}
			if fp != "" {
				n = ip + "." + fp
			}
		}
	}
	return n
}

// shiftDecimal multiplies the decimal text n by 10^places.
func shiftDecimal(n string, places int) string {
	neg := strings.HasPrefix(n, "-")
	n = strings.TrimPrefix(n, "-")
	ip, fp, _ := strings.Cut(n, ".")
	fp += strings.Repeat("0", max(0, places-len(fp)))
	ip, fp = ip+fp[:places], fp[places:]
	ip = strings.TrimLeft(ip, "0")
	if ip == "" {
		ip = "0"
	}
	out := ip
	if fp != "" {
		out += "." + fp
	}
	if neg && strings.Trim(out, "0.") != "" {
		out = "-" + out
	}
	return out
}

// sameNumber reports whether two decimal texts are one value.
func sameNumber(a, b string) bool {
	x, err1 := strconv.ParseFloat(plainNumber(a), 64)
	y, err2 := strconv.ParseFloat(plainNumber(b), 64)
	return err1 == nil && err2 == nil && x == y
}

func (f *formatter) pattern(p Pattern) string {
	var b strings.Builder
	for _, part := range p.Parts {
		switch {
		case part.Expr != nil:
			text := f.placeholder(part.Expr)
			if f.env.RTL() {
				text = "⁨" + text + "⁩"
			}
			b.WriteString(text)
		case part.Markup != nil:
		default:
			b.WriteString(f.env.Text(part.Text))
		}
	}
	return b.String()
}

func (f *formatter) placeholder(e *Expression) string {
	r := f.resolve(e)
	if !r.ok {
		if e.Operand != nil && e.Operand.Variable {
			return "{$" + e.Operand.Value + "}"
		}
		return "{�}"
	}
	if r.value.Kind == KindNoun && (r.function == nil || r.function.Name == "nomi:noun" || r.function.Name == "nomi:gender") {
		return f.noun(r)
	}
	if r.function == nil {
		return r.value.Text
	}
	switch r.function.Name {
	case "string":
		return r.value.Key
	case "integer", "number", "percent":
		return f.env.Number(f.operand(r), numberFormat(r))
	case "nomi:list":
		if r.value.Kind == KindList {
			return r.value.Text
		}
	case "currency":
		if r.value.Kind == KindMoney {
			return f.env.Money(r.value.Number, r.value.Key, moneyDisplay(r.function))
		}
	case "date", "time", "datetime":
		if calendarKind(r.value.Kind) {
			return f.env.Date(r.value, DateRequestOf(r.function, r.value.Kind))
		}
	}
	return "{" + r.name + "}"
}

// noun writes a noun value in the case and for the count `:nomi:noun`'s
// options name, the nominative and 1 when they name none.
func (f *formatter) noun(r resolved) string {
	kase, count := Nominative, "1"
	if r.function != nil && r.function.Name == "nomi:noun" {
		if o, ok := r.function.Option("case"); ok && !o.Value.Variable {
			kase = o.Value.Value
		}
		if o, ok := r.function.Option("count"); ok {
			if o.Value.Variable {
				if c := f.variable(o.Value.Value); c.ok && c.value.Number != "" {
					count = c.value.Number
				}
			} else {
				count = o.Value.Value
			}
		}
	}
	return f.env.Noun(r.value.Text, kase, count)
}

// Weekday is the day of the week of the ISO 8601 date text (`2026-10-20`
// is a `tuesday`), as `:nomi:weekday` names it, or "" for text that is no
// date.
func Weekday(date string) string {
	neg := strings.HasPrefix(date, "-")
	date = strings.TrimLeft(date, "+-")
	parts := strings.Split(date, "-")
	if len(parts) != 3 {
		return ""
	}
	var ymd [3]int64
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return ""
		}
		ymd[i] = n
	}
	y, m, d := ymd[0], ymd[1], ymd[2]
	if neg {
		y = -y
	}
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return ""
	}
	// Days from 1970-01-01 in the proleptic Gregorian calendar (Howard
	// Hinnant's days_from_civil).
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := era*146097 + doe - 719468
	// 1970-01-01 was a Thursday.
	i := (days%7 + 7 + 3) % 7
	return Weekdays[i]
}

func numberFormat(r resolved) NumberFormat {
	nf := NumberFormat{Grouping: "auto", MinInteger: 1, MaxFraction: -1}
	fn := r.function
	switch fn.Name {
	case "integer":
		nf.MaxFraction = 0
	case "percent":
		nf.Percent = true
		nf.MaxFraction = 0
	default:
		if r.value.Kind == KindFloat {
			nf.MaxFraction = 3
		}
	}
	for _, o := range fn.Options {
		n, _ := strconv.Atoi(o.Value.Value)
		switch o.Name {
		case "useGrouping":
			nf.Grouping = o.Value.Value
		case "minimumIntegerDigits":
			nf.MinInteger = n
		case "minimumFractionDigits":
			nf.MinFraction = n
			if nf.MaxFraction >= 0 && nf.MaxFraction < n {
				nf.MaxFraction = n
			}
		case "maximumFractionDigits":
			nf.MaxFraction = n
			if nf.MinFraction > n {
				nf.MinFraction = n
			}
		}
	}
	return nf
}
