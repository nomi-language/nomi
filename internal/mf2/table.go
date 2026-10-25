package mf2

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Table is a message as a program carries it: what the compiler checked,
// for the formatter to run. The function a `message` declaration expands
// into holds it as text (EncodeTable), so a program carries the texts of
// the messages it reaches and no others.
type Table struct {
	// Name is the message's qualified name (`shop.cart_items`).
	Name string `json:"name"`
	// Params are the parameters in declaration order, which is the order
	// the arguments come in, each with what its type is to a text: the
	// signature a catalog loaded at run time is checked against
	// (Signature).
	Params []Param `json:"params"`
	// Source is the source locale; Texts[Source] is the declaration's text.
	Source string `json:"source"`
	// Texts are each locale's canonical text: the source's and every
	// listed locale's current translation. A locale with none falls back
	// along its parents to the source.
	Texts map[string]string `json:"texts"`
}

// Signature is the message's signature, which its texts are checked
// against: its declared name (the part of Name after the file's) and its
// parameters.
func (t *Table) Signature() Signature {
	return Signature{Name: DeclName(t.Name), Params: t.Params}
}

// DeclName is a qualified name's declared part: `cart_items` of
// `cart.cart_items`.
func DeclName(qualified string) string {
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// EncodeTable writes t as text.
func EncodeTable(t Table) string {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// tables caches decoded tables and their parsed texts by their text: a
// program formats one message many times.
var tables sync.Map // string -> *decoded

type decoded struct {
	Table
	once   sync.Mutex
	parsed map[string]*Message
}

// DecodeTable reads text EncodeTable wrote, once per text.
func DecodeTable(text string) (*Table, error) {
	d, err := decodeTable(text)
	if err != nil {
		return nil, err
	}
	return &d.Table, nil
}

func decodeTable(text string) (*decoded, error) {
	if d, ok := tables.Load(text); ok {
		return d.(*decoded), nil
	}
	var t Table
	if err := json.Unmarshal([]byte(text), &t); err != nil {
		return nil, fmt.Errorf("a message table that does not decode: %w", err)
	}
	d := &decoded{Table: t, parsed: map[string]*Message{}}
	actual, _ := tables.LoadOrStore(text, d)
	return actual.(*decoded), nil
}

// TableMessage is the parsed text of locale in the table text, once per
// table and locale.
func TableMessage(text, locale string) (*Table, *Message, error) {
	d, err := decodeTable(text)
	if err != nil {
		return nil, nil, err
	}
	d.once.Lock()
	defer d.once.Unlock()
	if m, ok := d.parsed[locale]; ok {
		return &d.Table, m, nil
	}
	src, ok := d.Texts[locale]
	if !ok {
		return &d.Table, nil, fmt.Errorf("%s has no text in %s", d.Name, locale)
	}
	m, errs := Parse(src)
	if len(errs) > 0 {
		return &d.Table, nil, fmt.Errorf("%s's %s text does not parse: %s", d.Name, locale, errs[0].Message)
	}
	d.parsed[locale] = m
	return &d.Table, m, nil
}
