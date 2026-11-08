package tether

import (
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maulai/tether/internal/vtag"
)

// Validate checks v, a struct or a pointer to one, with the rules of its v
// tags and its enums, exactly as Route checks an input after reading it. It
// returns nil, or the field errors joined into one error: a handler can
// return it as it is to answer 422, and Issues lists them. It is for input
// that does not arrive over HTTP, and for tests of the rules.
//
// The generator registers the enums of every type passed to Validate, so
// the argument must have a named struct type where it is called, not any.
// Like Route at start-up, Validate panics if v is not a struct or one of
// its tags has a mistake.
func Validate(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		panic(fmt.Sprintf("tether: Validate needs a struct, not %T", v))
	}
	plan, err := structPlanFor(rv.Type())
	if err != nil {
		panic(fmt.Sprintf("tether: Validate: %s: %v", rv.Type(), err))
	}
	var errs []error
	for _, is := range validate(rv, plan) {
		errs = append(errs, &fieldError{is})
	}
	return errors.Join(errs...)
}

// validate checks v against its plan and returns every failed rule, at most
// one per value: a form shows one message per field, and the first failure
// is the one worth fixing first.
//
// The order is fixed, and the TypeScript runtime follows it: conditions,
// required, then the always-on text and enum checks, then the tag's rules
// in the order they are written, then the contents.
func validate(v reflect.Value, sp *structPlan) []Issue {
	var c checker
	c.structFields("", v, sp)
	return c.issues
}

type checker struct{ issues []Issue }

func (c *checker) add(path, code string, args map[string]string) {
	c.issues = append(c.issues, Issue{Field: path, Rule: code, Message: message(code, args)})
}

func (c *checker) structFields(path string, v reflect.Value, sp *structPlan) {
	for _, fp := range sp.fields {
		fv, ok := fieldByIndex(v, fp.index, false)
		if !ok {
			fv = reflect.Zero(fp.field.Type)
		}
		c.value(join(path, fp.name), fv, fp.plan, v, false)
	}
}

// value checks one value. parent is the struct holding it, for conditions;
// elem marks a slice or map element, which is never "not given": it was
// sent on purpose, so its rules apply even when it is empty.
func (c *checker) value(path string, v reflect.Value, vp *valuePlan, parent reflect.Value, elem bool) {
	isEmpty := empty(v, vp)
	for _, cd := range vp.conds {
		if !condMatches(cd, parent) {
			continue
		}
		if cd.name == "requiredif" && isEmpty || cd.name == "forbiddenif" && !isEmpty {
			c.add(path, cd.name, nil)
			return
		}
	}
	if vp.required && isEmpty {
		c.add(path, "required", nil)
		return
	}
	if vp.pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if !elem && absent(v, vp) {
		return
	}
	if vp.kind == vtag.String && v.Kind() == reflect.String {
		s := v.String()
		if badText(s) {
			c.add(path, "text", nil)
			return
		}
		if set := enumValues(vp.typ); set != nil && !set[s] {
			c.add(path, "enum", nil)
			return
		}
	}
	for _, r := range vp.rules {
		if !ruleHolds(r, v, vp) {
			c.add(path, r.code, ruleArgs(r))
			return
		}
	}
	switch vp.kind {
	case vtag.Slice:
		for i := range v.Len() {
			c.value(join(path, strconv.Itoa(i)), v.Index(i), vp.elem, reflect.Value{}, true)
		}
	case vtag.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, k := range keys {
			p := join(path, k.String())
			if badText(k.String()) {
				c.add(p, "text", nil)
				continue
			}
			if set := enumValues(vp.key.typ); set != nil && !set[k.String()] {
				c.add(p, "enum", nil)
				continue
			}
			c.value(p, v.MapIndex(k), vp.elem, reflect.Value{}, true)
		}
	case vtag.Struct:
		c.structFields(path, v, vp.fields)
	}
}

// empty answers "does the field have content?" for required and the
// conditions. Text that is only whitespace has none.
func empty(v reflect.Value, vp *valuePlan) bool {
	if vp.pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	switch vp.kind {
	case vtag.String:
		if v.Kind() == reflect.String {
			return strings.TrimSpace(v.String()) == ""
		}
		if v.Kind() == reflect.Slice {
			return v.Len() == 0
		}
		return v.IsZero()
	case vtag.Slice, vtag.Map:
		return v.Len() == 0
	case vtag.Time:
		return v.Interface().(time.Time).IsZero()
	case vtag.Any:
		return v.IsZero()
	}
	return false
}

// absent answers "was anything sent?", without trimming: " " was sent, so
// value rules such as email still see it. Numbers and booleans are never
// absent, because their zero value is a real value.
func absent(v reflect.Value, vp *valuePlan) bool {
	switch vp.kind {
	case vtag.String, vtag.Slice, vtag.Map:
		if k := v.Kind(); k == reflect.String || k == reflect.Slice || k == reflect.Map || k == reflect.Array {
			return v.Len() == 0
		}
		return v.IsZero()
	case vtag.Time, vtag.Any:
		return v.IsZero()
	}
	return false
}

func condMatches(cd cond, parent reflect.Value) bool {
	sp := cd.sibling.plan
	sib, ok := fieldByIndex(parent, cd.sibling.index, false)
	if !ok {
		sib = reflect.Zero(cd.sibling.field.Type)
	}
	var match bool
	switch {
	case cd.value == "":
		match = empty(sib, sp)
	case sp.pointer && sib.IsNil():
		match = false
	default:
		if sp.pointer {
			sib = sib.Elem()
		}
		switch sp.kind {
		case vtag.Bool:
			match = strconv.FormatBool(sib.Bool()) == cd.value
		case vtag.Int, vtag.Uint, vtag.Float:
			want, _ := strconv.ParseFloat(cd.value, 64)
			match = number(sib) == want
		default:
			match = text(sib) == cd.value
		}
	}
	return match != cd.negate
}

func ruleHolds(r rule, v reflect.Value, vp *valuePlan) bool {
	switch r.name {
	case "min":
		return measure(v, vp) >= r.num
	case "max":
		return measure(v, vp) <= r.num
	case "gt":
		return number(v) > r.num
	case "lt":
		return number(v) < r.num
	case "email":
		return emailRe.MatchString(text(v))
	case "url":
		return urlRe.MatchString(text(v))
	case "oneof":
		return slices.Contains(r.list, text(v))
	}
	pr, _ := lookupRule(r.name)
	if pr.re != nil {
		return pr.re.MatchString(text(v))
	}
	return pr.check(text(v))
}

func ruleArgs(r rule) map[string]string {
	switch r.name {
	case "min", "max", "gt", "lt":
		return map[string]string{r.name: r.arg}
	case "oneof":
		return map[string]string{"values": strings.Join(r.list, ", ")}
	}
	return nil
}

// measure is what min and max compare: characters of text, entries of a
// list, the value of a number. Characters, not bytes, because that is what
// a person counts and what the browser counts.
func measure(v reflect.Value, vp *valuePlan) float64 {
	switch vp.kind {
	case vtag.String:
		return float64(utf8.RuneCountInString(text(v)))
	case vtag.Slice, vtag.Map:
		return float64(v.Len())
	}
	return number(v)
}

func number(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	}
	return 0
}

// text is the string a rule sees: the string itself, the bytes of a
// []byte, or the text form of a type with MarshalText.
func text(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes())
		}
	}
	if m, ok := v.Interface().(encoding.TextMarshaler); ok {
		b, _ := m.MarshalText()
		return string(b)
	}
	return ""
}
