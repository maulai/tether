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
	keys     []string // of requiredkeys: entries a map must have
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
	// path leads to the field a comparison reads, from the struct beside
	// the value, or to the field unique compares, from each element.
	path []*fieldPlan
}

type cond struct {
	name   string // requiredif or forbiddenif
	field  string
	value  string
	negate bool
	path   []*fieldPlan // from the struct beside the value to the field read
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
	for _, link := range c.links {
		if err := link(); err != nil {
			return nil, err
		}
	}
	for typ, sp := range c.done {
		plans.LoadOrStore(typ, sp)
	}
	return p, nil
}

type compiler struct {
	done map[reflect.Type]*structPlan
	// links resolve the paths of rules that read other fields. They run
	// once every plan is complete, so a path into a type that is still
	// being compiled, a recursive one, finds all its fields.
	links []func() error
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
		n := len(c.links)
		vp, err := c.valuePlan(f.field.Type, tag, false)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t.Name(), f.name, err)
		}
		// A link the value added reports its mistake like the value would.
		for i := n; i < len(c.links); i++ {
			link, name := c.links[i], f.name
			c.links[i] = func() error {
				if err := link(); err != nil {
					return fmt.Errorf("%s.%s: %w", t.Name(), name, err)
				}
				return nil
			}
		}
		fp := &fieldPlan{jsonField: f, plan: vp}
		sp.fields = append(sp.fields, fp)
		sp.byName[f.name] = fp
	}
	for _, fp := range sp.fields {
		at := t.Name() + "." + fp.name
		for i := range fp.plan.conds {
			cd := &fp.plan.conds[i]
			c.links = append(c.links, func() error {
				path, err := resolvePath(sp, cd.name, cd.field)
				if err == nil {
					err = checkCondValue(cd, path[len(path)-1].plan)
				}
				if err != nil {
					return fmt.Errorf("%s: %w", at, err)
				}
				cd.path = path
				return nil
			})
		}
		for i := range fp.plan.rules {
			r := &fp.plan.rules[i]
			if vtag.Compare[r.name] == "" {
				continue
			}
			own := fp.plan.kind
			c.links = append(c.links, func() error {
				path, err := resolvePath(sp, r.name, r.arg)
				if err == nil {
					err = vtag.CheckCompare(r.name, own, r.arg, path[len(path)-1].plan.kind)
				}
				if err != nil {
					return fmt.Errorf("%s: %w", at, err)
				}
				r.path = path
				return nil
			})
		}
	}
	return sp, nil
}

// resolvePath finds the fields a path of JSON names leads through, from
// the struct sp: the field itself, then fields of the structs inside it.
func resolvePath(sp *structPlan, rule, path string) ([]*fieldPlan, error) {
	names, err := vtag.Path(path)
	if err != nil {
		return nil, err
	}
	var out []*fieldPlan
	for i, name := range names {
		if i > 0 {
			prev := out[i-1].plan
			if prev.kind != vtag.Struct {
				return nil, vtag.NotStruct(rule, path, strings.Join(names[:i], "."), prev.kind)
			}
			sp = prev.fields
		}
		fp, ok := sp.byName[name]
		if !ok {
			return nil, fmt.Errorf("%s refers to unknown field %q", rule, path)
		}
		out = append(out, fp)
	}
	return out, nil
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

// valuePlan compiles the plan of one value. elem marks the element of a
// slice or map, which has no fields beside it for a rule to read.
func (c *compiler) valuePlan(t reflect.Type, tag vtag.Tag, elem bool) (*valuePlan, error) {
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
		if elem && vtag.ReadsField(r.Name) {
			return nil, vtag.InElem(r.Name)
		}
		switch r.Name {
		case "required":
			vp.required = true
		case "requiredkeys":
			vp.keys = append(vp.keys, strings.Fields(r.Arg)...)
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
		elem, err := c.valuePlan(t.Elem(), elemTag, true)
		if err != nil {
			return nil, err
		}
		vp.elem = elem
		if err := c.unique(vp); err != nil {
			return nil, err
		}
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

// unique checks the unique rules of a list: its elements, or the field it
// names in each, must be values unique can compare.
func (c *compiler) unique(vp *valuePlan) error {
	elem := vp.elem
	for i := range vp.rules {
		r := &vp.rules[i]
		switch {
		case r.name != "unique":
		case r.arg == "":
			if err := vtag.CheckUnique("", elem.kind, isBytes(elem.typ)); err != nil {
				return err
			}
		case elem.kind != vtag.Struct:
			return vtag.NotStruct("unique", r.arg, "each element", elem.kind)
		default:
			c.links = append(c.links, func() error {
				path, err := resolvePath(elem.fields, "unique", r.arg)
				if err != nil {
					return err
				}
				last := path[len(path)-1].plan
				if err := vtag.CheckUnique(r.arg, last.kind, isBytes(last.typ)); err != nil {
					return err
				}
				r.path = path
				return nil
			})
		}
	}
	return nil
}

func isBytes(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}
