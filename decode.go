package tether

import (
	"bytes"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maulai/tether/internal/vtag"
)

var errMalformed = errors.New("malformed")

// decodeBody reads JSON into v. It walks the parsed document alongside the
// Go type instead of calling json.Unmarshal, so every problem gets the exact
// path of the value ("items.2.sku"), all problems are reported at once, and
// an empty string can mean "not given" for numbers and times, which is what
// forms send for an empty input.
//
// whole is set for PUT, which replaces the whole record: every field of
// every object must then be sent, so a field the caller forgot is an error
// instead of a value silently cleared.
func decodeBody(data []byte, v reflect.Value, sp *structPlan, whole bool) ([]Issue, error) {
	var doc any = map[string]any{}
	if len(bytes.TrimSpace(data)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, errMalformed
		}
		if _, err := dec.Token(); err != io.EOF {
			return nil, errMalformed
		}
	}
	if doc == nil && whole {
		doc = map[string]any{} // a null body sends no field either
	}
	d := decoder{whole: whole}
	d.value("", doc, v, &valuePlan{typ: v.Type(), kind: vtag.Struct, fields: sp}, false)
	return d.issues, nil
}

type decoder struct {
	issues []Issue
	whole  bool
}

func (d *decoder) fail(path, code string) {
	d.issues = append(d.issues, Issue{Field: path, Rule: code, Message: message(code, nil)})
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// value stores raw into v. asString is the ",string" option of the field,
// which puts a number or boolean inside a JSON string.
func (d *decoder) value(path string, raw any, v reflect.Value, vp *valuePlan, asString bool) {
	if raw == nil {
		return // JSON null leaves the zero value, as with encoding/json
	}
	if s, ok := raw.(string); ok && s == "" && (vp.kind.Number() || vp.kind == vtag.Time || hasFormat(vp.typ)) {
		return // forms send "" for an empty number, date or select
	}
	if vp.pointer {
		if v.IsNil() {
			v.Set(reflect.New(vp.typ))
		}
		v = v.Elem()
	}
	t := vp.typ
	switch {
	case t == timeType:
		s, ok := raw.(string)
		if !ok {
			d.fail(path, "type")
			return
		}
		tm, err := parseTime(s)
		if err != nil {
			d.fail(path, "type")
			return
		}
		v.Set(reflect.ValueOf(tm))
		return
	case t == rawMessageType:
		b, _ := json.Marshal(raw)
		v.SetBytes(b)
		return
	case reflect.PointerTo(t).Implements(jsonUnmarshaler):
		b, _ := json.Marshal(raw)
		if err := v.Addr().Interface().(json.Unmarshaler).UnmarshalJSON(b); err != nil {
			d.fail(path, "type")
		}
		return
	case reflect.PointerTo(t).Implements(textUnmarshaler):
		s, ok := raw.(string)
		if !ok {
			d.fail(path, "type")
			return
		}
		d.text(path, s, v)
		return
	}

	if asString && (vp.kind.Number() || vp.kind == vtag.Bool) {
		s, ok := raw.(string)
		if !ok {
			d.fail(path, "type")
			return
		}
		raw = json.Number(s)
		if vp.kind == vtag.Bool {
			b, err := strconv.ParseBool(s)
			if err != nil {
				d.fail(path, "type")
				return
			}
			raw = b
		}
	}

	switch vp.kind {
	case vtag.String:
		s, ok := raw.(string)
		if !ok {
			d.fail(path, "type")
			return
		}
		if t.Kind() != reflect.String { // []byte
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				d.fail(path, "type")
				return
			}
			v.SetBytes(b)
			return
		}
		v.SetString(s)
	case vtag.Bool:
		b, ok := raw.(bool)
		if !ok {
			d.fail(path, "type")
			return
		}
		v.SetBool(b)
	case vtag.Int, vtag.Uint, vtag.Float:
		n, ok := raw.(json.Number)
		if !ok {
			d.fail(path, "type")
			return
		}
		if code := setNumber(v, string(n)); code != "" {
			d.fail(path, code)
		}
	case vtag.Slice:
		arr, ok := raw.([]any)
		if !ok {
			d.fail(path, "type")
			return
		}
		if t.Kind() == reflect.Slice {
			v.Set(reflect.MakeSlice(t, len(arr), len(arr)))
		}
		for i, el := range arr {
			if i >= v.Len() {
				break // like encoding/json, an array keeps its length
			}
			d.value(join(path, strconv.Itoa(i)), el, v.Index(i), vp.elem, false)
		}
	case vtag.Map:
		obj, ok := raw.(map[string]any)
		if !ok {
			d.fail(path, "type")
			return
		}
		if v.IsNil() {
			v.Set(reflect.MakeMapWithSize(t, len(obj)))
		}
		for _, k := range sortedKeys(obj) {
			el := reflect.New(t.Elem()).Elem()
			d.value(join(path, k), obj[k], el, vp.elem, false)
			v.SetMapIndex(reflect.ValueOf(k).Convert(t.Key()), el)
		}
	case vtag.Struct:
		obj, ok := raw.(map[string]any)
		if !ok {
			d.fail(path, "type")
			return
		}
		keys := sortedKeys(obj)
		if d.whole {
			keys = withMissing(keys, obj, vp.fields)
		}
		for _, k := range keys {
			fp, ok := vp.fields.byName[k]
			if !ok {
				d.fail(join(path, k), "unknown")
				continue
			}
			raw, sent := obj[k]
			if !sent {
				d.fail(join(path, k), "missing")
				continue
			}
			fv, _ := fieldByIndex(v, fp.index, true)
			d.value(join(path, k), raw, fv, fp.plan, fp.asString)
		}
	case vtag.Any:
		v.Set(reflect.ValueOf(plain(raw)))
	default:
		d.fail(path, "type")
	}
}

// text reads s into a type with UnmarshalText. A registered Format is
// checked first, so the client gets its message, the same the browser
// shows, instead of a bare "type".
func (d *decoder) text(path, s string, v reflect.Value) {
	if f, ok := formatFor(v.Type()); ok && !f.re.MatchString(s) {
		d.fail(path, f.name)
		return
	}
	if err := v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
		d.fail(path, "type")
	}
}

func hasFormat(t reflect.Type) bool {
	_, ok := formatFor(t)
	return ok
}

// withMissing adds to the sorted keys of obj the fields it lacks, so they are
// reported in order with the rest. A field with omitempty or omitzero may be
// left out; null counts as sent.
func withMissing(keys []string, obj map[string]any, sp *structPlan) []string {
	for _, fp := range sp.fields {
		if _, ok := obj[fp.name]; !ok && !fp.omitEmpty {
			keys = append(keys, fp.name)
		}
	}
	sort.Strings(keys)
	return keys
}

// setNumber parses s into the number v and returns a message code on
// failure: "integer" for a fraction sent to an integer field, "type" for
// anything else that does not fit.
func setNumber(v reflect.Value, s string) string {
	bits := v.Type().Bits()
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, bits)
		if err != nil {
			return intProblem(s)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(s, 10, bits)
		if err != nil {
			return intProblem(s)
		}
		v.SetUint(n)
	default:
		f, err := strconv.ParseFloat(s, bits)
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return "type"
		}
		v.SetFloat(f)
	}
	return ""
}

func intProblem(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err == nil && f != math.Trunc(f) {
		return "integer"
	}
	return "type"
}

// parseTime accepts RFC 3339, the format encoding/json writes, and a plain
// date, which is what a date input yields; the date means midnight UTC.
func parseTime(s string) (time.Time, error) {
	if len(s) == len(time.DateOnly) {
		return time.Parse(time.DateOnly, s)
	}
	return time.Parse(time.RFC3339Nano, s)
}

// plain turns json.Number back into float64 for values typed as any, which
// is what encoding/json would have stored.
func plain(raw any) any {
	switch x := raw.(type) {
	case json.Number:
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = plain(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = plain(x[k])
		}
	}
	return raw
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// decodeQuery fills v from the query string. Every key must name a field;
// a field that is not a slice takes one value.
func decodeQuery(q url.Values, v reflect.Value, sp *structPlan) []Issue {
	var d decoder
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := q[k]
		fp, ok := sp.byName[k]
		if !ok {
			d.fail(k, "unknown")
			continue
		}
		fv, _ := fieldByIndex(v, fp.index, true)
		vp := fp.plan
		if vp.kind == vtag.Slice {
			if vp.pointer {
				fv.Set(reflect.New(vp.typ))
				fv = fv.Elem()
			}
			fv.Set(reflect.MakeSlice(vp.typ, len(vals), len(vals)))
			for i, s := range vals {
				d.scalar(join(k, strconv.Itoa(i)), s, fv.Index(i), vp.elem)
			}
			continue
		}
		if len(vals) > 1 {
			d.fail(k, "type")
			continue
		}
		d.scalar(k, vals[0], fv, vp)
	}
	return d.issues
}

// scalar parses one query value. An empty value means "not given".
func (d *decoder) scalar(path, s string, v reflect.Value, vp *valuePlan) {
	if s == "" {
		return
	}
	if vp.pointer {
		v.Set(reflect.New(vp.typ))
		v = v.Elem()
	}
	t := vp.typ
	switch {
	case t == timeType:
		tm, err := parseTime(s)
		if err != nil {
			d.fail(path, "type")
			return
		}
		v.Set(reflect.ValueOf(tm))
		return
	case reflect.PointerTo(t).Implements(textUnmarshaler):
		d.text(path, s, v)
		return
	}
	switch vp.kind {
	case vtag.String:
		if t.Kind() != reflect.String {
			d.fail(path, "type")
			return
		}
		v.SetString(s)
	case vtag.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			d.fail(path, "type")
			return
		}
		v.SetBool(b)
	case vtag.Int, vtag.Uint, vtag.Float:
		if code := setNumber(v, strings.TrimSpace(s)); code != "" {
			d.fail(path, code)
		}
	default:
		d.fail(path, "type")
	}
}

// queryable reports whether a field can be read from the query string:
// scalars and slices of scalars.
func queryable(vp *valuePlan) bool {
	scalar := func(p *valuePlan) bool {
		switch p.kind {
		case vtag.String:
			return p.typ.Kind() == reflect.String || p.typ == timeType || reflect.PointerTo(p.typ).Implements(textUnmarshaler)
		case vtag.Bool, vtag.Int, vtag.Uint, vtag.Float, vtag.Time:
			return true
		}
		return false
	}
	if vp.kind == vtag.Slice {
		return vp.typ.Kind() == reflect.Slice && !vp.elem.pointer && scalar(vp.elem)
	}
	return scalar(vp)
}
