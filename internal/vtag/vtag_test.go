package vtag

import (
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse("required, max=5 ,dive,required,oneof=a b,dive,min=1")
	if err != nil {
		t.Fatal(err)
	}
	want := Tag{
		Rules: []Rule{{"required", ""}, {"max", "5"}},
		Elem: &Tag{
			Rules: []Rule{{"required", ""}, {"oneof", "a b"}},
			Elem:  &Tag{Rules: []Rule{{"min", "1"}}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	for _, bad := range []string{"required,,max=1", "max=", "dive=1", "9lives"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		rule Rule
		f    Field
		err  string
	}{
		{Rule{"required", ""}, Field{Kind: Int}, "use a pointer"},
		{Rule{"required", ""}, Field{Kind: Int, Pointer: true}, ""},
		{Rule{"required", ""}, Field{Kind: Struct}, "always present"},
		{Rule{"max", "1.5"}, Field{Kind: Int}, "whole number"},
		{Rule{"max", "1.5"}, Field{Kind: Float}, ""},
		{Rule{"min", "-1"}, Field{Kind: String}, "0 or more"},
		{Rule{"gt", "0"}, Field{Kind: String}, "does not apply to a string"},
		{Rule{"email", ""}, Field{Kind: Int}, "does not apply to an integer"},
		{Rule{"requiredif", "kind"}, Field{Kind: String}, "field:value"},
		{Rule{"slug", ""}, Field{Kind: String}, "unknown rule"},
		{Rule{"slug", ""}, Field{Kind: String, Project: func(string) bool { return true }}, ""},
		{Rule{"forbiddenif", "kind:x"}, Field{Kind: Int}, ""},
		{Rule{"forbiddenif", "kind:x"}, Field{Kind: Bool}, ""},
		{Rule{"requiredif", "kind:x"}, Field{Kind: Bool}, "use a pointer"},
		{Rule{"requiredif", "target.purpose:x"}, Field{Kind: String}, ""},
		{Rule{"requiredif", "target..purpose:x"}, Field{Kind: String}, "path of field names"},
		{Rule{"unique", ""}, Field{Kind: Slice}, ""},
		{Rule{"unique", "sku"}, Field{Kind: Slice}, ""},
		{Rule{"unique", ""}, Field{Kind: Map}, "does not apply to a map"},
		{Rule{"requiredkeys", "de en"}, Field{Kind: Map}, ""},
		{Rule{"requiredkeys", "de"}, Field{Kind: Slice}, "does not apply to a slice"},
		{Rule{"minfield", "from"}, Field{Kind: Time}, ""},
		{Rule{"gtfield", "low"}, Field{Kind: Float, Pointer: true}, ""},
		{Rule{"ltfield", "a."}, Field{Kind: Int}, "path of field names"},
		{Rule{"maxfield", "to"}, Field{Kind: String}, "does not apply to a string"},
	}
	for _, c := range cases {
		err := Check(c.rule, c.f)
		switch {
		case c.err == "" && err != nil:
			t.Errorf("%v on %v: %v", c.rule, c.f.Kind, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%v on %v: got %v, want %q", c.rule, c.f.Kind, err, c.err)
		}
	}
}

func TestShared(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{CheckCompare("minfield", Time, "from", Int), "compares a time with from, an integer"},
		{CheckCompare("minfield", Int, "from", Float), ""},
		{CheckUnique("", Struct, false), "each element is a struct"},
		{CheckUnique("sku", Time, false), "sku is a time"},
		{CheckUnique("", String, true), "each element is a []byte"},
		{CheckUnique("", String, false), ""},
	} {
		switch {
		case c.want == "" && c.err != nil:
			t.Errorf("unexpected %v", c.err)
		case c.want != "" && (c.err == nil || !strings.Contains(c.err.Error(), c.want)):
			t.Errorf("got %v, want %q", c.err, c.want)
		}
	}
}

func TestCode(t *testing.T) {
	if Code("min", String) != "minLength" || Code("max", Slice) != "maxItems" || Code("max", Int) != "max" || Code("gt", Float) != "gt" {
		t.Error("wrong codes")
	}
}
