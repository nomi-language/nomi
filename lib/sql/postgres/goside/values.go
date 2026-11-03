//go:build !js

package goside

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// mismatchError is a value that cannot cross.
type mismatchError string

func (e mismatchError) Error() string { return string(e) }

const (
	dateLayout      = "2006-01-02"
	timeLayout      = "15:04:05.999999999"
	timestampLayout = "2006-01-02T15:04:05.999999999"
)

// values is the parameters as pgx binds them.
func values(params []Cell) ([]any, Failure) {
	out := make([]any, len(params))
	for i, p := range params {
		v, err := param(p.Kind, p.Int, p.Float, p.Text, p.Bytes)
		if err == nil && p.Kind == KindArray {
			v, err = array(p.Items)
		}
		if err != nil {
			return nil, Failure{Kind: FailDatabase, Code: "22000", Message: fmt.Sprintf("parameter $%d: %v", i+1, err)}
		}
		out[i] = v
	}
	return out, Failure{}
}

// param is one value as pgx binds it. An array's elements are bound by
// array.
func param(kind, n int64, x float64, text string, bytes []byte) (any, error) {
	switch kind {
	case KindNull, KindArray:
		return nil, nil
	case KindInt:
		return n, nil
	case KindFloat:
		return x, nil
	case KindText:
		return text, nil
	case KindBytes:
		if bytes == nil {
			return []byte{}, nil
		}
		return bytes, nil
	case KindDecimal:
		var d pgtype.Numeric
		if err := d.Scan(text); err != nil {
			return nil, fmt.Errorf("the Decimal %s: %w", text, err)
		}
		return d, nil
	case KindBool:
		return n != 0, nil
	case KindDate:
		t, err := time.Parse(dateLayout, text)
		if err != nil {
			return nil, err
		}
		return pgtype.Date{Time: t, Valid: true}, nil
	case KindTime:
		t, err := time.Parse(timeLayout, text)
		if err != nil {
			return nil, err
		}
		since := t.Sub(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC))
		return pgtype.Time{Microseconds: since.Microseconds(), Valid: true}, nil
	case KindTimestamp:
		t, err := time.Parse(timestampLayout, text)
		if err != nil {
			return nil, err
		}
		return pgtype.Timestamp{Time: t, Valid: true}, nil
	case KindTimestamptz:
		return pgtype.Timestamptz{Time: time.Unix(0, n).UTC(), Valid: true}, nil
	case KindUuid:
		var u pgtype.UUID
		if err := u.Scan(text); err != nil {
			return nil, fmt.Errorf("the Uuid %s: %w", text, err)
		}
		return u, nil
	case KindJson:
		// Raw JSON text, which pgx sends as is to a json or jsonb
		// parameter.
		return []byte(text), nil
	}
	return nil, fmt.Errorf("a value of kind %d has no Postgres form", kind)
}

// array is an Array parameter as a Go slice of one element type, which
// pgx binds as the matching Postgres array. A NULL element is a nil
// pointer; an empty array is an empty text array, which Postgres casts to
// the array type the statement wants.
func array(items []Item) (any, error) {
	kind := KindNull
	for _, it := range items {
		if it.Kind == KindNull {
			continue
		}
		if kind != KindNull && it.Kind != kind {
			return nil, mismatchError("an Array's elements must all be of one kind")
		}
		kind = it.Kind
	}
	switch kind {
	case KindNull:
		return pointers[string](items, kind)
	case KindInt:
		return pointers[int64](items, kind)
	case KindFloat:
		return pointers[float64](items, kind)
	case KindText:
		return pointers[string](items, kind)
	case KindBytes:
		return pointers[[]byte](items, kind)
	case KindDecimal:
		return pointers[pgtype.Numeric](items, kind)
	case KindBool:
		return pointers[bool](items, kind)
	case KindDate:
		return pointers[pgtype.Date](items, kind)
	case KindTime:
		return pointers[pgtype.Time](items, kind)
	case KindTimestamp:
		return pointers[pgtype.Timestamp](items, kind)
	case KindTimestamptz:
		return pointers[pgtype.Timestamptz](items, kind)
	case KindUuid:
		return pointers[pgtype.UUID](items, kind)
	case KindJson:
		return pointers[[]byte](items, kind)
	}
	return nil, fmt.Errorf("an Array of kind %d has no Postgres form", kind)
}

// pointers is items as a []*T, nil for a NULL element.
func pointers[T any](items []Item, kind int64) ([]*T, error) {
	out := make([]*T, len(items))
	for i, it := range items {
		if it.Kind == KindNull {
			continue
		}
		v, err := param(it.Kind, it.Int, it.Float, it.Text, it.Bytes)
		if err != nil {
			return nil, err
		}
		t, ok := v.(T)
		if !ok {
			return nil, fmt.Errorf("element %d does not bind as %T", i+1, *new(T))
		}
		out[i] = &t
	}
	return out, nil
}

// Postgres type OIDs the driver reads by: what Values answers for these
// is not enough to tell the type (a time.Time is a date, a timestamp or a
// timestamptz), and json and jsonb are read raw.
const (
	oidJSON        = 114
	oidJSONB       = 3802
	oidDate        = 1082
	oidTimestamp   = 1114
	oidTimestamptz = 1184
	oidJSONArray   = 199
	oidJSONBArray  = 3807
	oidDateArray   = 1182
	oidTsArray     = 1115
	oidTstzArray   = 1185
)

// elementOID is the element type of the array type oid, for the arrays
// whose element read depends on its type, or 0.
func elementOID(oid uint32) uint32 {
	switch oid {
	case oidJSONArray:
		return oidJSON
	case oidJSONBArray:
		return oidJSONB
	case oidDateArray:
		return oidDate
	case oidTsArray:
		return oidTimestamp
	case oidTstzArray:
		return oidTimestamptz
	}
	return 0
}

// cellOf is one column's value, decoded by pgx as v, with raw its bytes
// on the wire.
func cellOf(v any, raw []byte, field pgconn.FieldDescription) (Cell, error) {
	switch field.DataTypeOID {
	case oidJSON, oidJSONB:
		if v == nil {
			return Cell{Kind: KindNull}, nil
		}
		text := raw
		if field.Format == pgtype.BinaryFormatCode && field.DataTypeOID == oidJSONB && len(text) > 0 {
			// Binary jsonb is a version byte, then the text.
			text = text[1:]
		}
		return Cell{Kind: KindJson, Text: string(text)}, nil
	}
	if list, ok := v.([]any); ok {
		items := make([]Item, len(list))
		for i, e := range list {
			c, err := scalar(e, elementOID(field.DataTypeOID), field.Name)
			if err != nil {
				return Cell{}, err
			}
			if c.Kind == KindArray {
				return Cell{}, mismatchError(fmt.Sprintf("the column `%s` holds an array of more than one dimension, which has no Nomi form", field.Name))
			}
			items[i] = Item{Kind: c.Kind, Int: c.Int, Float: c.Float, Text: c.Text, Bytes: c.Bytes}
		}
		return Cell{Kind: KindArray, Items: items}, nil
	}
	return scalar(v, field.DataTypeOID, field.Name)
}

// scalar is a value that is not a whole array, of the type oid when the
// read depends on it.
func scalar(v any, oid uint32, column string) (Cell, error) {
	switch v := v.(type) {
	case nil:
		return Cell{Kind: KindNull}, nil
	case int16:
		return Cell{Kind: KindInt, Int: int64(v)}, nil
	case int32:
		return Cell{Kind: KindInt, Int: int64(v)}, nil
	case int64:
		return Cell{Kind: KindInt, Int: v}, nil
	case float32:
		return Cell{Kind: KindFloat, Float: float64(v)}, nil
	case float64:
		return Cell{Kind: KindFloat, Float: v}, nil
	case string:
		if oid == oidJSON || oid == oidJSONB {
			return Cell{Kind: KindJson, Text: v}, nil
		}
		return Cell{Kind: KindText, Text: v}, nil
	case bool:
		n := int64(0)
		if v {
			n = 1
		}
		return Cell{Kind: KindBool, Int: n}, nil
	case []byte:
		return Cell{Kind: KindBytes, Bytes: append([]byte{}, v...)}, nil
	case pgtype.Numeric:
		return numeric(v, column)
	case time.Time:
		switch oid {
		case oidDate:
			return Cell{Kind: KindDate, Text: v.Format(dateLayout)}, nil
		case oidTimestamp:
			return Cell{Kind: KindTimestamp, Text: v.Format(timestampLayout)}, nil
		}
		return Cell{Kind: KindTimestamptz, Int: v.UnixNano()}, nil
	case pgtype.Time:
		t := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(v.Microseconds) * time.Microsecond)
		return Cell{Kind: KindTime, Text: t.Format(timeLayout)}, nil
	case [16]byte:
		u := pgtype.UUID{Bytes: v, Valid: true}
		text, _ := u.Value()
		return Cell{Kind: KindUuid, Text: text.(string)}, nil
	case []any:
		return Cell{Kind: KindArray}, nil
	case map[string]any:
		return Cell{}, mismatchError(fmt.Sprintf("the column `%s` holds a composite value, which has no Nomi form; select its fields", column))
	}
	// Any other type (an interval, an inet, a range) reads as its text.
	return Cell{Kind: KindText, Text: fmt.Sprint(v)}, nil
}

// numeric is a numeric column's value: a Decimal, or a Float for NaN and
// the infinities, which a Decimal does not hold.
func numeric(n pgtype.Numeric, column string) (Cell, error) {
	switch {
	case !n.Valid:
		return Cell{Kind: KindNull}, nil
	case n.NaN:
		return Cell{Kind: KindFloat, Float: math.NaN()}, nil
	case n.InfinityModifier == pgtype.Infinity:
		return Cell{Kind: KindFloat, Float: math.Inf(1)}, nil
	case n.InfinityModifier == pgtype.NegativeInfinity:
		return Cell{Kind: KindFloat, Float: math.Inf(-1)}, nil
	}
	return Cell{Kind: KindDecimal, Text: decimalText(n.Int, n.Exp)}, nil
}

// decimalText writes digits × 10^exp as decimal text, with as many
// decimals as -exp.
func decimalText(digits *big.Int, exp int32) string {
	s := new(big.Int).Abs(digits).String()
	sign := ""
	if digits.Sign() < 0 {
		sign = "-"
	}
	if exp >= 0 {
		return sign + s + strings.Repeat("0", int(exp))
	}
	scale := int(-exp)
	if len(s) <= scale {
		s = strings.Repeat("0", scale-len(s)+1) + s
	}
	return sign + s[:len(s)-scale] + "." + s[len(s)-scale:]
}
