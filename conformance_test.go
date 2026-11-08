package tether

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/maulai/tether/internal/jsregex"
)

// The conformance vectors are shared with the TypeScript runtime, which
// runs the same file. A case that passes here and fails there, or the other
// way round, is a disagreement between server and browser.

type color string

type vectors struct {
	Patterns map[string]struct{ Go, JS, Message string } `json:"patterns"`
	Checks   []string                                    `json:"checks"`
	Cases    []vectorCase                                `json:"cases"`
}

type vectorCase struct {
	Name    string          `json:"name"`
	Fields  []vectorField   `json:"fields"`
	Input   json.RawMessage `json:"input"`
	Errors  []vectorError   `json:"errors"`
	Browser *[]vectorError  `json:"browser"`
}

type vectorField struct {
	Name   string        `json:"name"`
	Type   string        `json:"type"`
	Tag    string        `json:"tag"`
	Fields []vectorField `json:"fields"`
}

// vectorError is one expected error. Message is compared only where a case
// gives it.
type vectorError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message,omitempty"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/conformance/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMain(m *testing.M) {
	RegisterEnum(color("red"), color("green"), color("blue"))
	data, err := os.ReadFile("testdata/conformance/rules.json")
	if err != nil {
		panic(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		panic(err)
	}
	for name, p := range v.Patterns {
		Pattern(name, p.Go, p.Message)
	}
	for _, name := range v.Checks {
		Check(name, func(s string) bool { return len(s)%2 == 0 }, "Needs an even length.")
	}
	os.Exit(m.Run())
}

func TestConformance(t *testing.T) {
	v := loadVectors(t)
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			typ := vectorType(c.Fields)
			plan, err := structPlanFor(typ)
			if err != nil {
				t.Fatal(err)
			}
			in := reflect.New(typ).Elem()
			issues, err := decodeBody(c.Input, in, plan, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(issues) == 0 {
				issues = validate(in, plan)
			}
			want := c.Errors
			if want == nil {
				want = []vectorError{}
			}
			got := []vectorError{}
			for i, is := range issues {
				e := vectorError{Field: is.Field, Rule: is.Rule}
				if i < len(want) && want[i].Message != "" {
					e.Message = is.Message
				}
				got = append(got, e)
				if is.Message == "" {
					t.Errorf("%s: empty message", is.Field)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

// TestConformancePatterns pins the JavaScript form of every pattern the
// vectors use, so the TypeScript side tests the translation tether really
// produces.
func TestConformancePatterns(t *testing.T) {
	for name, p := range loadVectors(t).Patterns {
		js, err := jsregex.Translate(p.Go)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if js != p.JS {
			t.Errorf("%s: translates to %q, the vectors say %q", name, js, p.JS)
		}
	}
}

func vectorType(fields []vectorField) reflect.Type {
	var sfs []reflect.StructField
	for i, f := range fields {
		sfs = append(sfs, reflect.StructField{
			Name: fmt.Sprintf("F%d", i),
			Type: vectorGoType(f),
			Tag:  reflect.StructTag(fmt.Sprintf(`json:"%s" v:"%s"`, f.Name, f.Tag)),
		})
	}
	return reflect.StructOf(sfs)
}

func vectorGoType(f vectorField) reflect.Type {
	switch f.Type {
	case "string":
		return reflect.TypeFor[string]()
	case "*string":
		return reflect.TypeFor[*string]()
	case "int":
		return reflect.TypeFor[int]()
	case "*int":
		return reflect.TypeFor[*int]()
	case "int8":
		return reflect.TypeFor[int8]()
	case "uint":
		return reflect.TypeFor[uint]()
	case "float64":
		return reflect.TypeFor[float64]()
	case "*float64":
		return reflect.TypeFor[*float64]()
	case "bool":
		return reflect.TypeFor[bool]()
	case "time":
		return reflect.TypeFor[time.Time]()
	case "color":
		return reflect.TypeFor[color]()
	case "[]string":
		return reflect.TypeFor[[]string]()
	case "[]color":
		return reflect.TypeFor[[]color]()
	case "map[string]string":
		return reflect.TypeFor[map[string]string]()
	case "map[color]string":
		return reflect.TypeFor[map[color]string]()
	case "[]byte":
		return reflect.TypeFor[[]byte]()
	case "object":
		return vectorType(f.Fields)
	case "[]object":
		return reflect.SliceOf(vectorType(f.Fields))
	}
	panic("unknown vector type " + f.Type)
}
