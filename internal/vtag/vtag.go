// Package vtag parses and checks the v struct tag. The server and the
// generator both use it, so a tag means the same thing on both sides and a
// mistake is reported with the same words.
package vtag

import (
	"fmt"
	"strconv"
	"strings"
)

// Rule is one entry of a tag: a name and an optional argument.
type Rule struct {
	Name string
	Arg  string
}

// Tag is a parsed v tag. Rules apply to the field itself. Elem holds the
// rules after a dive, which apply to each element of a slice or map; a
// second dive nests another level.
type Tag struct {
	Rules []Rule
	Elem  *Tag
}

// Parse splits a tag into rules. It checks only the syntax; whether a rule
// fits the field's type is Check's job, because that needs the type.
func Parse(s string) (Tag, error) {
	var root Tag
	if strings.TrimSpace(s) == "" {
		return root, nil
	}
	cur := &root
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return Tag{}, fmt.Errorf("empty rule in %q", s)
		}
		name, arg, hasArg := strings.Cut(tok, "=")
		name, arg = strings.TrimSpace(name), strings.TrimSpace(arg)
		if name == "dive" {
			if hasArg {
				return Tag{}, fmt.Errorf("dive takes no argument")
			}
			cur.Elem = &Tag{}
			cur = cur.Elem
			continue
		}
		if !IsName(name) {
			return Tag{}, fmt.Errorf("invalid rule name %q", name)
		}
		if hasArg && arg == "" {
			return Tag{}, fmt.Errorf("rule %s has an empty argument", name)
		}
		cur.Rules = append(cur.Rules, Rule{Name: name, Arg: arg})
	}
	return root, nil
}

// Has reports whether the tag's own rules contain name.
func (t Tag) Has(name string) bool {
	for _, r := range t.Rules {
		if r.Name == name {
			return true
		}
	}
	return false
}

// IsName reports whether s can name a rule: a letter followed by letters
// and digits. Project rules share the namespace, so the same test applies.
func IsName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		digit := c >= '0' && c <= '9'
		if !letter && !(digit && i > 0) {
			return false
		}
	}
	return true
}

// Kind is the shape of a Go type as far as the rules care. Both the
// reflect based server and the go/types based generator reduce their types
// to it, so applicability is decided in one place.
type Kind int

const (
	Other Kind = iota
	String
	Int
	Uint
	Float
	Bool
	Slice
	Map
	Struct
	Time
	Any
)

func (k Kind) String() string {
	switch k {
	case String:
		return "string"
	case Int, Uint:
		return "integer"
	case Float:
		return "number"
	case Bool:
		return "boolean"
	case Slice:
		return "slice"
	case Map:
		return "map"
	case Struct:
		return "struct"
	case Time:
		return "time"
	case Any:
		return "any value"
	}
	return "unsupported type"
}

// phrase is the kind with its article, for messages.
func (k Kind) phrase() string {
	switch k {
	case Int, Uint, Other:
		return "an " + k.String()
	case Any:
		return k.String()
	}
	return "a " + k.String()
}

// Number reports whether k holds a number.
func (k Kind) Number() bool { return k == Int || k == Uint || k == Float }

// Builtin lists the rule names tether defines itself. Project rules must
// not reuse them.
var Builtin = map[string]bool{
	"required": true, "requiredif": true, "forbiddenif": true,
	"min": true, "max": true, "gt": true, "lt": true,
	"email": true, "url": true, "oneof": true,
}

// Field describes the value a rule is checked against.
type Field struct {
	Kind Kind
	// Pointer is true for a pointer field. It makes a number or boolean
	// optional and lets required tell "missing" from "zero".
	Pointer bool
	// Project reports whether a project rule with this name exists.
	Project func(name string) bool
}

// Check reports whether rule r fits a value of field f, and whether its
// argument is well formed.
func Check(r Rule, f Field) error {
	k := f.Kind
	switch r.Name {
	case "required", "requiredif", "forbiddenif":
		if r.Name != "required" {
			if _, _, _, err := ParseCond(r.Arg); err != nil {
				return err
			}
		} else if r.Arg != "" {
			return fmt.Errorf("required takes no argument")
		}
		if f.Pointer {
			return nil
		}
		switch k {
		case String, Slice, Map, Time, Any:
			return nil
		case Int, Uint, Float, Bool:
			return fmt.Errorf("%s on a plain %s cannot tell a missing value from %s; use a pointer, or min to forbid zero", r.Name, k, zeroOf(k))
		case Struct:
			return fmt.Errorf("%s on a struct has no effect, a struct is always present; use a pointer", r.Name)
		}
	case "min", "max":
		switch k {
		case String, Slice, Map:
			_, err := parseCount(r)
			return err
		case Int, Uint, Float:
			_, err := ParseNumber(r.Arg, k)
			return err
		}
	case "gt", "lt":
		if k.Number() {
			_, err := ParseNumber(r.Arg, k)
			return err
		}
	case "email", "url":
		if r.Arg != "" {
			return fmt.Errorf("%s takes no argument", r.Name)
		}
		if k == String {
			return nil
		}
	case "oneof":
		if len(strings.Fields(r.Arg)) == 0 {
			return fmt.Errorf("oneof needs at least one value")
		}
		if k == String {
			return nil
		}
	default:
		if f.Project == nil || !f.Project(r.Name) {
			return fmt.Errorf("unknown rule %q", r.Name)
		}
		if r.Arg != "" {
			return fmt.Errorf("rule %s takes no argument", r.Name)
		}
		if k == String {
			return nil
		}
	}
	return fmt.Errorf("rule %s does not apply to %s", r.Name, k.phrase())
}

func zeroOf(k Kind) string {
	if k == Bool {
		return "false"
	}
	return "0"
}

func parseCount(r Rule) (int, error) {
	n, err := strconv.Atoi(r.Arg)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s needs a whole number of 0 or more, got %q", r.Name, r.Arg)
	}
	return n, nil
}

// ParseNumber reads the argument of min, max, gt and lt for a number field.
// Integer fields need an integer argument, so the comparison stays exact.
func ParseNumber(arg string, k Kind) (float64, error) {
	if k == Int || k == Uint {
		n, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("an integer field needs a whole number, got %q", arg)
		}
		return float64(n), nil
	}
	f, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q", arg)
	}
	return f, nil
}

// ParseCond splits the argument of requiredif and forbiddenif, "field:value".
// A leading "!" on the value negates the comparison; an empty value means
// "is empty".
func ParseCond(arg string) (field, value string, negate bool, err error) {
	field, value, ok := strings.Cut(arg, ":")
	if !ok || field == "" {
		return "", "", false, fmt.Errorf("a condition needs the form field:value, got %q", arg)
	}
	if strings.HasPrefix(value, "!") {
		return field, value[1:], true, nil
	}
	return field, value, false, nil
}

// Code is the message code a rule reports for a field of kind k. min and max
// mean length on text and entry count on lists, so they get their own codes:
// the sentence differs.
func Code(rule string, k Kind) string {
	if rule != "min" && rule != "max" {
		return rule
	}
	switch k {
	case String:
		return rule + "Length"
	case Slice, Map:
		return rule + "Items"
	}
	return rule
}
