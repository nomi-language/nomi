package mf2

import (
	"fmt"
	"strconv"
	"strings"
)

// What an editor says about a text's syntax on hover: each function a text
// may call, with its options, and each declaration keyword. The table is
// the one place these are described; TestFunctionDocsMatchValidate holds
// it to the functions and options Validate accepts.

// FunctionDoc describes one function a text may call.
type FunctionDoc struct {
	// Name is the function's name without its colon, its namespace
	// included: `integer`, `nomi:noun`.
	Name string
	// Summary says what a placeholder with the function writes.
	Summary string
	// Takes names the parameter types it formats.
	Takes string
	// Selects says how a `.match` on a variable it annotates picks an
	// arm; empty for a function that selects nothing.
	Selects string
	// Options are its options in the order they are listed.
	Options []OptionDoc
	// Later reports a function MF2 defines that Nomi does not support yet.
	Later bool
}

// OptionDoc describes one option of a function.
type OptionDoc struct {
	Name, Doc string
}

// Nomi reports one of Nomi's own functions, in the `nomi:` namespace.
func (f FunctionDoc) Nomi() bool { return strings.HasPrefix(f.Name, "nomi:") }

// Option is f's option named name.
func (f FunctionDoc) Option(name string) (OptionDoc, bool) {
	for _, o := range f.Options {
		if o.Name == name {
			return o, true
		}
	}
	return OptionDoc{}, false
}

var (
	selectOption      = OptionDoc{"select", "How the value selects under `.match`: `plural` (the default) by its plural category, `ordinal` by its ordinal category (1st, 2nd, 3rd), `exact` by its exact value alone."}
	groupingOption    = OptionDoc{"useGrouping", "Whether the number is written with grouping separators (`1,234`): `auto` (the default) as the locale writes it, `always`, `never`, or `min2`, only from five digits on."}
	minIntegerOption  = OptionDoc{"minimumIntegerDigits", "The fewest integer digits written, padded with zeros: `minimumIntegerDigits=2` writes 5 as `05`. From 1 to 20."}
	minFractionOption = OptionDoc{"minimumFractionDigits", "The fewest fraction digits written, padded with zeros. From 0 to 20. The padded digits count for the plural category: in English `1` is `one` and `1.0` is `other`."}
	maxFractionOption = OptionDoc{"maximumFractionDigits", "The most fraction digits written; the number is rounded half to even to them. From 0 to 20."}
	numberSelects     = "by the number's plural category in the locale (`one`, `few`, `other`, ...). A key that is a number (`0`) matches that value exactly and wins over a category."
	dateTimeFields    = func(fields ...string) []OptionDoc {
		var out []OptionDoc
		for _, f := range fields {
			out = append(out, OptionDoc{f, fmt.Sprintf("Writes the %s, as %s. The fields named make a CLDR skeleton the locale writes in its own order.", fieldWhat[f], orList(quoteAll(DateTimeOptions[f])))})
		}
		return out
	}
	fieldWhat = map[string]string{
		"weekday": "day of the week", "era": "era", "year": "year", "month": "month", "day": "day of the month",
		"hour": "hour", "minute": "minute", "second": "second", "fractionalSecondDigits": "fraction of the second",
	}
)

var functionDocs = []FunctionDoc{
	{Name: "string", Summary: "Writes a `String` as it is, or the name of an enum's variant.",
		Takes:   "`String`, or an enum whose variants carry nothing",
		Selects: "by the exact value: a `String` by its text, an enum by its variant's name. A text that selects by an enum needs an arm for each variant."},
	{Name: "number", Summary: "Writes a number with the locale's digits, separators and grouping.",
		Takes: "`Int`, `Float` or `Decimal`", Selects: numberSelects,
		Options: []OptionDoc{selectOption, groupingOption, minIntegerOption, minFractionOption, maxFractionOption}},
	{Name: "integer", Summary: "Writes a number as a whole number, its fraction cut off, with the locale's digits, separators and grouping.",
		Takes: "`Int`", Selects: numberSelects + " Only the categories some whole number is in need an arm.",
		Options: []OptionDoc{selectOption, groupingOption, minIntegerOption}},
	{Name: "percent", Summary: "Writes a number times 100 as a percentage in the locale: `0.25` is `25%`.",
		Takes: "`Int`, `Float` or `Decimal`", Selects: "by the plural category of the number times 100, as `:number` does.",
		Options: []OptionDoc{selectOption, groupingOption, minIntegerOption, minFractionOption, maxFractionOption}},
	{Name: "currency", Summary: "Writes a `Money`'s amount with its currency, as `Money.format` does.",
		Takes:   "`Money` (std::money)",
		Options: []OptionDoc{{"currencyDisplay", "How the currency is shown: `symbol` (the default, `$`), `narrowSymbol`, `code` (`USD`) or `name` (`US dollars`)."}}},
	{Name: "date", Summary: "Writes the date of a calendar value at a style.",
		Takes:   "`Date`, `NaiveDateTime` or `DateTime` (std::calendar)",
		Options: []OptionDoc{{"style", "How much of the date is written: `full`, `long`, `medium` (the default) or `short`."}}},
	{Name: "time", Summary: "Writes the time of day of a calendar value at a style.",
		Takes:   "`Time`, `NaiveDateTime` or `DateTime` (std::calendar)",
		Options: []OptionDoc{{"style", "How much of the time is written: `full`, `long`, `medium` or `short` (the default)."}}},
	{Name: "datetime", Summary: "Writes a calendar value at its `dateStyle` and `timeStyle`, or by the fields its options name; with neither, the date at `medium` and the time at `short`.",
		Takes: "`Date`, `Time`, `NaiveDateTime` or `DateTime` (std::calendar)",
		Options: append([]OptionDoc{
			{"dateStyle", "How much of the date is written: `full`, `long`, `medium` or `short`. Not beside the fields."},
			{"timeStyle", "How much of the time of day is written: `full`, `long`, `medium` or `short`. Not beside the fields."},
		}, append(dateTimeFields("weekday", "era", "year", "month", "day", "hour", "minute", "second", "fractionalSecondDigits"),
			OptionDoc{"hourCycle", "The clock the hour is written on: `h11` and `h12` (12 hours), `h23` and `h24` (24 hours). The locale's own when left out."})...)},
	{Name: "nomi:noun", Summary: "Writes a `Noun` in the form for its case and count, from the noun's text in the locale of this text.",
		Takes: "`Noun`",
		Options: []OptionDoc{
			{"case", "The grammatical case the noun is written in: one of the locale's cases, `nominative` when left out. A catalog gives each noun a form per case."},
			{"count", "How many of the noun: a whole number or an `Int` parameter, 1 when left out. It is the `$count` the noun's text selects its plural form by."},
		}},
	{Name: "nomi:gender", Summary: "Selects by a `Noun`'s grammatical gender; a placeholder with it writes the noun's nominative for one.",
		Takes:   "`Noun`",
		Selects: "by the noun's gender in the locale: the keys are the language's genders, and a text needs an arm for each gender its nouns have."},
	{Name: "nomi:list", Summary: "Writes a `List<String>` joined as `String.join_list` does in the locale (`a, b and c`).",
		Takes:   "`List<String>`",
		Selects: "by the list's length, as a plural selector of an `Int` does."},
	{Name: "nomi:weekday", Summary: "Selects by a `Date`'s day of the week; it writes nothing.",
		Takes:   "`Date` (std::calendar)",
		Selects: "by the day of the week: the keys are `monday` through `sunday`, and a selector that names one day needs all seven."},
	{Name: "offset", Summary: "MessageFormat 2 defines `:offset`; Nomi does not support it yet.", Later: true},
	{Name: "unit", Summary: "MessageFormat 2 defines `:unit`; Nomi does not support it yet.", Later: true},
}

// FunctionDocOf is the doc of the function named name (`integer`,
// `nomi:noun`).
func FunctionDocOf(name string) (FunctionDoc, bool) {
	for _, f := range functionDocs {
		if f.Name == name {
			return f, true
		}
	}
	return FunctionDoc{}, false
}

// FunctionDocs are every function's doc, in the table's order.
func FunctionDocs() []FunctionDoc { return functionDocs }

// KeywordDocs say what each declaration keyword does.
var KeywordDocs = map[string]string{
	".input": "`.input {$name :function options}` annotates a parameter: every use of `$name` in the text formats and selects with that function.",
	".local": "`.local $name = {expression}` names a value for the rest of the text: a parameter or literal with a function and options.",
	".match": "`.match $name ...` picks one variant by the selectors' values: each variant's keys are matched in order, and the variant of `*` keys is the fallback.",
}

// PluralExamples are the numbers rules put in category, as ranges:
// `2–4, 22–24, 32–34, …` (Polish `few`), `1` (English `one`). integers
// keeps to whole numbers; otherwise a category with none shows
// fractions (`0.5, 1.5`). Empty when nothing sampled is in it.
func PluralExamples(r Rules, category string, integers bool) string {
	const runs = 3
	var nums []int
	for i := 0; i <= 1000; i++ {
		if r.CategoryOf(strconv.Itoa(i)) == category {
			nums = append(nums, i)
		}
	}
	if len(nums) == 0 {
		if integers {
			return ""
		}
		return strings.Join(examples(r, category, false), ", ")
	}
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if len(parts) == runs {
			return strings.Join(parts, ", ") + ", …"
		}
		switch {
		case nums[j] == 1000:
			// A run to the end of the sample goes on.
			first := nums[i]
			parts = append(parts, fmt.Sprintf("%d, %d, %d, …", first, first+1, first+2))
			return strings.Join(parts, ", ")
		case j-i >= 2:
			parts = append(parts, fmt.Sprintf("%d–%d", nums[i], nums[j]))
		case j == i+1:
			parts = append(parts, strconv.Itoa(nums[i]), strconv.Itoa(nums[j]))
		default:
			parts = append(parts, strconv.Itoa(nums[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}
