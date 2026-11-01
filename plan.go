package tether

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maulai/tether/internal/vtag"
)

var (
	timeType        = reflect.TypeFor[time.Time]()
	rawMessageType  = reflect.TypeFor[json.RawMessage]()
	uploadType      = reflect.TypeFor[Upload]()
	fileType        = reflect.TypeFor[File]()
	jsonMarshaler   = reflect.TypeFor[json.Marshaler]()
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textMarshaler   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
	errorType       = reflect.TypeFor[error]()
)

// valuePlan is everything tether needs to decode and validate values of one
// type at one place: the field's own rules, and plans for what it contains.
type valuePlan struct {
	typ     reflect.Type // without the pointer
	pointer bool
	kind    vtag.Kind

	required bool
	conds    []cond
	rules    []rule

	elem   *valuePlan  // slice, array and map elements
	key    *valuePlan  // map keys
	fields *structPlan // struct fields
}

type rule struct {
	name string
	code string
	arg  string
	num  float64
	list []string
}

type cond struct {
	name    string // requiredif or forbiddenif
	field   string
	value   string
	negate  bool
	sibling *fieldPlan
}

type structPlan struct {
	fields []*fieldPlan
	byName map[string]*fieldPlan
}

type fieldPlan struct {
	jsonField
	plan *valuePlan
}

var plans sync.Map // reflect.Type -> *structPlan

// structPlanFor compiles the plan for a struct type once. Mistakes in tags
// are returned as errors so Route can stop the program at start-up.
func structPlanFor(t reflect.Type) (*structPlan, error) {
	if p, ok := plans.Load(t); ok {
		return p.(*structPlan), nil
	}
	c := compiler{done: map[reflect.Type]*structPlan{}}
	p, err := c.structPlan(t)
	if err != nil {
		return nil, err
	}
	for typ, sp := range c.done {
		plans.LoadOrStore(typ, sp)
	}
	return p, nil
}

type compiler struct {
	done map[reflect.Type]*structPlan
}

func (c *compiler) structPlan(t reflect.Type) (*structPlan, error) {
	if p, ok := c.done[t]; ok {
		return p, nil
	}
	if p, ok := plans.Load(t); ok {
		return p.(*structPlan), nil
	}
	sp := &structPlan{byName: map[string]*fieldPlan{}}
	// Registered before the fields are compiled, so a recursive type finds
	// its own plan instead of recursing forever.
	c.done[t] = sp
	fields, err := jsonFields(t)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		tag, err := vtag.Parse(f.field.Tag.Get("v"))
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name(), f.name, err)
		}
		vp, err := c.valuePlan(f.field.Type, tag)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name(), f.name, err)
		}
		fp := &fieldPlan{jsonField: f, plan: vp}
		sp.fields = append(sp.fields, fp)
		sp.byName[f.name] = fp
	}
	for _, fp := range sp.fields {
		for i := range fp.plan.conds {
			cd := &fp.plan.conds[i]
			sib, ok := sp.byName[cd.field]
			if !ok {
				return nil, fmt.Errorf("%s.%s: %s refers to unknown field %q", t.Name(), fp.name, cd.name, cd.field)
			}
			if err := checkCondValue(cd, sib.plan); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", t.Name(), fp.name, err)
			}
			cd.sibling = sib
		}
	}
	return sp, nil
}

func checkCondValue(cd *cond, sib *valuePlan) error {
	if cd.value == "" {
		return nil
	}
	switch {
	case sib.kind == vtag.Bool:
		if cd.value != "true" && cd.value != "false" {
			return fmt.Errorf("%s compares boolean %s with %q; use true, false or nothing", cd.name, cd.field, cd.value)
		}
	case sib.kind.Number():
		if _, err := strconv.ParseFloat(cd.value, 64); err != nil {
			return fmt.Errorf("%s compares number %s with %q", cd.name, cd.field, cd.value)
		}
	case sib.kind != vtag.String:
		return fmt.Errorf("%s can compare %s only with emptiness", cd.name, cd.field)
	}
	return nil
}

// kindOf reduces a type to what the rules care about.
func kindOf(t reflect.Type) vtag.Kind {
	switch {
	case t == timeType:
		return vtag.Time
	case t == rawMessageType:
		return vtag.Any
	case t.Implements(textMarshaler) || reflect.PointerTo(t).Implements(textUnmarshaler):
		return vtag.String
	case t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler):
		return vtag.Any
	}
	switch t.Kind() {
	case reflect.String:
		return vtag.String
	case reflect.Bool:
		return vtag.Bool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return vtag.Int
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return vtag.Uint
	case reflect.Float32, reflect.Float64:
		return vtag.Float
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return vtag.String // []byte travels as base64 text; [N]byte does not
		}
		return vtag.Slice
	case reflect.Map:
		return vtag.Map
	case reflect.Struct:
		return vtag.Struct
	case reflect.Interface:
		if t.NumMethod() == 0 {
			return vtag.Any
		}
	}
	return vtag.Other
}

// decodable reports whether tether can fill a value of t from JSON. Types
// that only know how to write themselves (a MarshalText or MarshalJSON
// without the matching Unmarshal) cannot be inputs.
func decodable(t reflect.Type, k vtag.Kind) bool {
	switch {
	case t == timeType || t == rawMessageType:
		return true
	case reflect.PointerTo(t).Implements(jsonUnmarshaler), reflect.PointerTo(t).Implements(textUnmarshaler):
		return true
	case k == vtag.Any:
		return t.Kind() == reflect.Interface
	case k == vtag.String:
		return t.Kind() == reflect.String || t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
	}
	return true
}

func (c *compiler) valuePlan(t reflect.Type, tag vtag.Tag) (*valuePlan, error) {
	vp := &valuePlan{}
	if t.Kind() == reflect.Pointer {
		vp.pointer = true
		t = t.Elem()
		if t.Kind() == reflect.Pointer {
			return nil, fmt.Errorf("pointers to pointers are not supported")
		}
	}
	vp.typ = t
	vp.kind = kindOf(t)
	if vp.kind == vtag.Other || !decodable(t, vp.kind) {
		return nil, fmt.Errorf("type %s cannot be read from JSON", t)
	}
	field := vtag.Field{Kind: vp.kind, Pointer: vp.pointer, Project: projectRuleExists}
	for _, r := range tag.Rules {
		if err := vtag.Check(r, field); err != nil {
			return nil, err
		}
		switch r.Name {
		case "required":
			vp.required = true
		case "requiredif", "forbiddenif":
			f, v, neg, _ := vtag.ParseCond(r.Arg)
			vp.conds = append(vp.conds, cond{name: r.Name, field: f, value: v, negate: neg})
		default:
			ru := rule{name: r.Name, code: vtag.Code(r.Name, vp.kind), arg: r.Arg}
			switch r.Name {
			case "min", "max", "gt", "lt":
				if vp.kind.Number() {
					ru.num, _ = vtag.ParseNumber(r.Arg, vp.kind)
				} else {
					n, _ := strconv.Atoi(r.Arg)
					ru.num = float64(n)
				}
			case "oneof":
				ru.list = strings.Fields(r.Arg)
			}
			vp.rules = append(vp.rules, ru)
		}
	}

	switch vp.kind {
	case vtag.Slice, vtag.Map:
		var elemTag vtag.Tag
		if tag.Elem != nil {
			elemTag = *tag.Elem
		}
		elem, err := c.valuePlan(t.Elem(), elemTag)
		if err != nil {
			return nil, err
		}
		vp.elem = elem
		if vp.kind == vtag.Map {
			if t.Key().Kind() != reflect.String {
				return nil, fmt.Errorf("map keys must be strings, not %s", t.Key())
			}
			vp.key = &valuePlan{typ: t.Key(), kind: vtag.String}
		}
	case vtag.Struct:
		if tag.Elem != nil {
			return nil, fmt.Errorf("dive applies to slices and maps only")
		}
		sp, err := c.structPlan(t)
		if err != nil {
			return nil, err
		}
		vp.fields = sp
	default:
		if tag.Elem != nil {
			return nil, fmt.Errorf("dive applies to slices and maps only")
		}
	}
	return vp, nil
}
