// Package collgen builds the root collation and the per-locale tailorings
// that package collation compares with, from the pinned CLDR release's
// FractionalUCA.txt (the root collation, with gaps between its weights for
// tailoring) and its collation/*.xml rules. internal/cldrgen runs it;
// nothing a program runs links it.
//
// The rule compiler (builder.go) is a port of ICU4C's CollationBuilder,
// which is what CLDR's rules are written against: a tailoring's resets and
// relations become nodes in a list beside the root's weights, and each
// tailored node gets weights between its neighbours'.
package collgen

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/collation"
)

// Fractional is FractionalUCA.txt, read.
type Fractional struct {
	Version string
	// Mappings are the root's mappings, without the U+FDD0 contractions
	// FractionalUCA uses only to name weights.
	Mappings []collation.Mapping
	// Markers are every mapping FractionalUCA lists, the U+FDD0 and U+FDD1
	// contractions included: what a reset to a special position finds.
	Markers []collation.Mapping
	// Positions are the special positions it names ("first regular",
	// "last variable", ...), each a CE.
	Positions   map[string]collation.CE
	VariableTop uint32
	Groups      []collation.Group
	Unified     [][2]rune
}

// ReadFractional reads FractionalUCA.txt (or FractionalUCA_SHORT.txt).
func ReadFractional(r io.Reader) (*Fractional, error) {
	f := &Fractional{Positions: map[string]collation.CE{}}
	var tops []topByte
	var markers []marker
	var pending []struct {
		key, prefix string
		ces         string
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		comment := ""
		if i := strings.Index(text, "#"); i >= 0 {
			comment = strings.TrimSpace(text[i+1:])
			text = text[:i]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		bad := func(err string) error { return fmt.Errorf("FractionalUCA line %d: %s", line, err) }
		if strings.HasPrefix(text, "[") {
			body := strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
			body = strings.TrimSpace(body)
			switch {
			case strings.HasPrefix(body, "UCA version = "):
				f.Version = strings.TrimSpace(strings.TrimPrefix(body, "UCA version = "))
			case strings.HasPrefix(body, "Unified_Ideograph "):
				for _, r := range strings.Fields(strings.TrimPrefix(body, "Unified_Ideograph ")) {
					lo, hi, _ := strings.Cut(r, "..")
					if hi == "" {
						hi = lo
					}
					a, err1 := strconv.ParseUint(lo, 16, 32)
					b, err2 := strconv.ParseUint(hi, 16, 32)
					if err1 != nil || err2 != nil {
						return nil, bad("bad Unified_Ideograph range " + r)
					}
					f.Unified = append(f.Unified, [2]rune{rune(a), rune(b)})
				}
			case strings.HasPrefix(body, "top_byte"):
				fs := strings.Fields(body)
				b, err := strconv.ParseUint(fs[1], 16, 8)
				if err != nil {
					return nil, bad("bad top_byte")
				}
				var codes []string
				for _, c := range fs[2:] {
					if c != "COMPRESS" {
						codes = append(codes, c)
					}
				}
				tops = append(tops, topByte{int(b), codes})
			case strings.HasPrefix(body, "variable top = "):
				p, err := parseWeight(strings.TrimPrefix(body, "variable top = "), 4)
				if err != nil {
					return nil, bad(err.Error())
				}
				f.VariableTop = p
			case strings.HasPrefix(body, "first ") || strings.HasPrefix(body, "last "):
				i := strings.Index(body, "[")
				if i < 0 {
					return nil, bad("position without a CE")
				}
				name := strings.TrimSpace(body[:i])
				ces, err := parseCEs(strings.ReplaceAll(body[i:], "X", "05"))
				if err != nil || len(ces) != 1 {
					return nil, bad(fmt.Sprintf("bad CE for [%s]", name))
				}
				f.Positions[name] = ces[0]
			}
			continue
		}
		key, ces, ok := strings.Cut(text, ";")
		if !ok {
			return nil, bad("no `;`")
		}
		prefix := ""
		if p, s, ok := strings.Cut(key, "|"); ok {
			prefix, key = p, s
		}
		ks, err := codePoints(key)
		if err != nil {
			return nil, bad(err.Error())
		}
		ps, err := codePoints(prefix)
		if err != nil {
			return nil, bad(err.Error())
		}
		pending = append(pending, struct {
			key, prefix string
			ces         string
		}{ks, ps, strings.TrimSpace(ces)})
		if strings.Contains(comment, "first primary") && strings.HasPrefix(ks, "﷑") {
			cs, err := parseCEs(strings.TrimSpace(ces))
			if err != nil || len(cs) != 1 {
				return nil, bad("bad script first primary")
			}
			name, _, _ := strings.Cut(comment, " first primary")
			markers = append(markers, marker{cs[0].Primary(), name})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	root := &collation.Root{Unified: f.Unified}
	sort.Slice(root.Unified, func(i, j int) bool { return root.Unified[i][0] < root.Unified[j][0] })
	f.Unified = root.Unified
	for _, m := range pending {
		ces, err := parseCEsWith(m.ces, root)
		if err != nil {
			return nil, fmt.Errorf("FractionalUCA %U: %v", []rune(m.key), err)
		}
		mp := collation.Mapping{Prefix: m.prefix, Str: m.key, CEs: ces}
		f.Markers = append(f.Markers, mp)
		if r := []rune(m.key); len(r) > 1 && (r[0] == 0xFDD0 || r[0] == 0xFDD1) {
			continue
		}
		f.Mappings = append(f.Mappings, mp)
	}
	groups, err := scriptGroups(tops, markers)
	if err != nil {
		return nil, err
	}
	f.Groups = groups
	return f, nil
}

// scriptGroups pairs the "first primary" markers, in order, with the
// reorder codes the top_byte lines name: one group per marker, holding the
// codes of its lead bytes in order. A lead byte that names as many scripts
// as it has markers gives each marker one; one that names several scripts
// for one marker (Hani Hans Hant) gives it them all.
type topByte struct {
	b     int
	codes []string
}

type marker struct {
	p    uint32
	name string
}

func scriptGroups(tops []topByte, markers []marker) ([]collation.Group, error) {
	special := map[string]string{"SPACE": "space", "PUNCTUATION": "punct", "SYMBOL": "symbol", "CURRENCY": "currency", "DIGIT": "digit"}
	codesOf := map[int][]string{}
	for _, t := range tops {
		codesOf[t.b] = t.codes
	}
	byLead := map[int][]int{}
	var leads []int
	for i, m := range markers {
		lead := int(m.p >> 24)
		if _, ok := byLead[lead]; !ok {
			leads = append(leads, lead)
		}
		byLead[lead] = append(byLead[lead], i)
	}
	var groups []collation.Group
	for _, lead := range leads {
		ms := byLead[lead]
		codes := codesOf[lead]
		if len(ms) > 1 && len(ms) != len(codes) && markers[ms[0]].p == markers[ms[len(ms)-1]].p {
			// Several markers at one primary (HIRAGANA and KATAKANA) are
			// one group naming every code.
			ms = ms[:1]
		}
		for k, i := range ms {
			m := markers[i]
			if s, ok := special[m.name]; ok {
				groups = append(groups, collation.Group{Start: m.p, Codes: []string{s}})
				continue
			}
			var cs []string
			switch {
			case len(codes) == len(ms):
				cs = []string{codes[k]}
			case len(ms) == 1:
				cs = codes
			case m.name == "HAN":
				cs = codes
			default:
				return nil, fmt.Errorf("lead byte %02X names %v for %d scripts", lead, codes, len(ms))
			}
			// Two markers at one primary (Meroitic) are one group.
			if n := len(groups); n > 0 && groups[n-1].Start == m.p {
				groups[n-1].Codes = append(groups[n-1].Codes, cs...)
				continue
			}
			groups = append(groups, collation.Group{Start: m.p, Codes: cs})
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Start < groups[j].Start })
	return groups, nil
}

func codePoints(s string) (string, error) {
	var b strings.Builder
	for _, f := range strings.Fields(s) {
		v, err := strconv.ParseUint(f, 16, 32)
		if err != nil {
			return "", fmt.Errorf("bad code point %q", f)
		}
		b.WriteRune(rune(v))
	}
	return b.String(), nil
}

// parseWeight reads a weight's hex bytes, left-aligned in size bytes.
func parseWeight(s string, size int) (uint32, error) {
	fs := strings.Fields(s)
	if len(fs) > size {
		return 0, fmt.Errorf("weight %q is longer than %d bytes", s, size)
	}
	var w uint32
	for i, f := range fs {
		b, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			return 0, fmt.Errorf("bad weight %q", s)
		}
		w |= uint32(b) << (8 * (size - 1 - i))
	}
	return w, nil
}

func parseCEs(s string) ([]collation.CE, error) { return parseCEsWith(s, nil) }

// parseCEsWith reads a run of `[p, s, t]` CEs. `[U+4E00]` is the implicit CE
// root gives the code point, and `[U+4E00, t]` or `[U+4E00, s, t]` that CE
// with another tertiary, or secondary and tertiary.
func parseCEsWith(s string, root *collation.Root) ([]collation.CE, error) {
	var out []collation.CE
	s = strings.TrimSpace(s)
	for s != "" {
		if s[0] != '[' {
			return nil, fmt.Errorf("bad CEs %q", s)
		}
		end := strings.Index(s, "]")
		if end < 0 {
			return nil, fmt.Errorf("bad CEs %q", s)
		}
		fields := strings.Split(s[1:end], ",")
		s = strings.TrimSpace(s[end+1:])
		if ref, ok := strings.CutPrefix(strings.TrimSpace(fields[0]), "U+"); ok {
			if root == nil {
				return nil, fmt.Errorf("a U+ reference here")
			}
			v, err := strconv.ParseUint(ref, 16, 32)
			if err != nil {
				return nil, fmt.Errorf("bad reference %q", ref)
			}
			ce := root.Implicit(rune(v))
			sec, ter := uint32(ce.Secondary()), uint32(ce.Lower16())
			switch len(fields) {
			case 2:
				t, err := parseWeight(fields[1], 2)
				if err != nil {
					return nil, err
				}
				ter = t
			case 3:
				sw, err1 := parseWeight(fields[1], 2)
				t, err2 := parseWeight(fields[2], 2)
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("bad CE %q", fields)
				}
				sec, ter = sw, t
			}
			out = append(out, collation.MakeCE(ce.Primary(), uint16(sec), uint16(ter)))
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("bad CE %q", fields)
		}
		p, err1 := parseWeight(fields[0], 4)
		sw, err2 := parseWeight(fields[1], 2)
		t, err3 := parseWeight(fields[2], 2)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("bad CE %q", fields)
		}
		out = append(out, collation.MakeCE(p, uint16(sw), uint16(t)))
	}
	return out, nil
}

// Root is the root collation FractionalUCA gives.
func (f *Fractional) Root() *collation.Root {
	end := f.Positions["last implicit"].Primary()
	return &collation.Root{
		Table:       collation.NewTable(f.Mappings),
		VariableTop: f.VariableTop,
		Groups:      f.Groups,
		GroupsEnd:   end,
		Unified:     f.Unified,
	}
}
