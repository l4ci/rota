// Package jsonx decodes JSON with object key order preserved and encodes it
// byte-for-byte the way Python's json.dumps(data, indent=2) does, so state
// files written by rota and by the old bin/ helpers never diff against each other.
//
// Decoded values are *Object, []any, string, json.Number, bool or nil.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Object is a JSON object that remembers key insertion order. A repeated
// key keeps its first position and takes the last value, as a Python dict does.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject returns an empty Object.
func NewObject() *Object { return &Object{vals: map[string]any{}} }

// Keys returns the keys in insertion order.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// Len is the number of keys.
func (o *Object) Len() int { return len(o.keys) }

// Get returns the value for key.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Set adds key at the end, or replaces its value in place.
func (o *Object) Set(key string, v any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = v
}

// Delete removes key.
func (o *Object) Delete(key string) {
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Decode parses one JSON document. Trailing non-space data is an error.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("jsonx: trailing data after JSON document")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := NewObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("jsonx: object key is %T", kt)
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				obj.Set(key, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("jsonx: unexpected delimiter %v", t)
	default:
		return tok, nil
	}
}

// Marshal encodes v as json.dumps(v, indent=2) would, without a trailing
// newline. v may hold the decoded types plus map[string]any (keys sorted),
// []string, int, int64 and float64.
func Marshal(v any) ([]byte, error) {
	var b strings.Builder
	if err := encode(&b, v, 0); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// MarshalCompact encodes v on one line with Python's default separators
// (", " and ": "), as json.dumps(v) would.
func MarshalCompact(v any) ([]byte, error) {
	var b strings.Builder
	if err := encode(&b, v, -1); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// encode writes v; level < 0 means compact.
func encode(b *strings.Builder, v any, level int) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeString(b, t)
	case json.Number:
		s, err := pyNumber(t)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case float64:
		b.WriteString(PyFloat(t))
	case *Object:
		return encodeObject(b, t.keys, func(k string) any { return t.vals[k] }, level)
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return encodeObject(b, keys, func(k string) any { return t[k] }, level)
	case []any:
		return encodeArray(b, len(t), func(i int) any { return t[i] }, level)
	case []string:
		return encodeArray(b, len(t), func(i int) any { return t[i] }, level)
	default:
		return fmt.Errorf("jsonx: cannot encode %T", v)
	}
	return nil
}

func encodeObject(b *strings.Builder, keys []string, get func(string) any, level int) error {
	if len(keys) == 0 {
		b.WriteString("{}")
		return nil
	}
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
			if level < 0 {
				b.WriteByte(' ')
			}
		}
		newline(b, next(level))
		writeString(b, k)
		b.WriteString(": ")
		if err := encode(b, get(k), next(level)); err != nil {
			return err
		}
	}
	newline(b, level)
	b.WriteByte('}')
	return nil
}

func encodeArray(b *strings.Builder, n int, get func(int) any, level int) error {
	if n == 0 {
		b.WriteString("[]")
		return nil
	}
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
			if level < 0 {
				b.WriteByte(' ')
			}
		}
		newline(b, next(level))
		if err := encode(b, get(i), next(level)); err != nil {
			return err
		}
	}
	newline(b, level)
	b.WriteByte(']')
	return nil
}

// next is the indent level for children: compact stays compact.
func next(level int) int {
	if level < 0 {
		return -1
	}
	return level + 1
}

// newline starts an indented line; compact output (level < 0, or the
// level+1 of a compact parent) writes nothing.
func newline(b *strings.Builder, level int) {
	if level < 0 {
		return
	}
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("  ", level))
}

// writeString escapes like Python's json with ensure_ascii=True.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || (r > 0x7e && r <= 0xffff):
				fmt.Fprintf(b, `\u%04x`, r)
			case r > 0xffff:
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// pyNumber renders a decoded number as Python would after json.loads:
// integers keep all their digits, everything else goes through float repr.
func pyNumber(n json.Number) (string, error) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		neg := strings.HasPrefix(s, "-")
		digits := strings.TrimLeft(strings.TrimPrefix(s, "-"), "0")
		if digits == "" {
			return "0", nil
		}
		if neg {
			return "-" + digits, nil
		}
		return digits, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return "", err
	}
	return PyFloat(f), nil
}

// PyFloat formats f like Python's repr(float): the shortest round-trip
// digits, fixed notation for exponents in [-4, 16), scientific otherwise
// with a sign and at least two exponent digits.
func PyFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case math.IsNaN(f):
		return "NaN"
	}
	sign := ""
	if math.Signbit(f) {
		sign = "-"
		f = -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	digits := strings.Replace(mant, ".", "", 1)
	if f == 0 {
		return sign + "0.0"
	}
	decpt := exp + 1
	if decpt > -4 && decpt <= 16 {
		switch {
		case decpt <= 0:
			return sign + "0." + strings.Repeat("0", -decpt) + digits
		case decpt >= len(digits):
			return sign + digits + strings.Repeat("0", decpt-len(digits)) + ".0"
		default:
			return sign + digits[:decpt] + "." + digits[decpt:]
		}
	}
	m := digits[:1]
	if len(digits) > 1 {
		m += "." + digits[1:]
	}
	es := "+"
	if exp < 0 {
		es = "-"
		exp = -exp
	}
	return fmt.Sprintf("%s%se%s%02d", sign, m, es, exp)
}

// Str reads a string field, "" when absent, null or not a string.
func Str(o *Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// Bool reads a bool field, false when absent, null or not a bool.
func Bool(o *Object, key string) bool {
	v, _ := o.Get(key)
	b, _ := v.(bool)
	return b
}

// Int reads a decoded JSON number (int, float64 or json.Number) as an int; ok
// is false for anything else.
func Int(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case float64:
		return int(t), true
	case json.Number:
		f, err := t.Float64()
		return int(f), err == nil
	}
	return 0, false
}
