package tether

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// jsonField is a struct field as encoding/json sees it: its JSON name and
// the index path to reach it, through embedded structs.
type jsonField struct {
	name      string
	index     []int
	field     reflect.StructField
	omitEmpty bool
	asString  bool
}

// jsonFields lists the fields of t the way encoding/json flattens them:
// embedded structs without a JSON name contribute their fields at the
// same level, a shallower field hides a deeper one with the same name.
// encoding/json silently drops names that stay ambiguous; tether reports
// them, because a field that vanishes from the API is a bug.
func jsonFields(t reflect.Type) ([]jsonField, error) {
	type candidate struct {
		jsonField
		depth  int
		tagged bool
	}
	var all []candidate
	var walk func(t reflect.Type, index []int, depth int, seen map[reflect.Type]bool) error
	walk = func(t reflect.Type, index []int, depth int, seen map[reflect.Type]bool) error {
		if seen[t] {
			return nil
		}
		seen[t] = true
		defer delete(seen, t)
		for i := range t.NumField() {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			idx := append(append([]int(nil), index...), i)
			if sf.Anonymous && name == "" {
				ft := sf.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					if !sf.IsExported() && sf.Type.Kind() == reflect.Pointer {
						continue
					}
					if err := walk(ft, idx, depth+1, seen); err != nil {
						return err
					}
					continue
				}
			}
			if !sf.IsExported() {
				continue
			}
			c := candidate{depth: depth, tagged: name != ""}
			if name == "" {
				name = sf.Name
			}
			c.name, c.index, c.field = name, idx, sf
			for _, o := range strings.Split(opts, ",") {
				switch o {
				case "omitempty", "omitzero":
					c.omitEmpty = true
				case "string":
					c.asString = true
				}
			}
			all = append(all, c)
		}
		return nil
	}
	if err := walk(t, nil, 0, map[reflect.Type]bool{}); err != nil {
		return nil, err
	}

	byName := map[string][]candidate{}
	var order []string
	for _, c := range all {
		if _, ok := byName[c.name]; !ok {
			order = append(order, c.name)
		}
		byName[c.name] = append(byName[c.name], c)
	}
	var out []jsonField
	for _, name := range order {
		cs := byName[name]
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].depth < cs[j].depth })
		top := cs[:1]
		for _, c := range cs[1:] {
			if c.depth == cs[0].depth {
				top = append(top, c)
			}
		}
		if len(top) > 1 {
			var tagged []candidate
			for _, c := range top {
				if c.tagged {
					tagged = append(tagged, c)
				}
			}
			if len(tagged) != 1 {
				return nil, fmt.Errorf("%s: JSON name %q is ambiguous between embedded fields", t, name)
			}
			top = tagged
		}
		out = append(out, top[0].jsonField)
	}
	sort.SliceStable(out, func(i, j int) bool { return lessIndex(out[i].index, out[j].index) })
	return out, nil
}

func lessIndex(a, b []int) bool {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// fieldByIndex returns the field at index, allocating nil embedded pointers
// on the way when alloc is set. Without alloc, a nil embedded pointer
// yields ok == false: the field is not there.
func fieldByIndex(v reflect.Value, index []int, alloc bool) (reflect.Value, bool) {
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !alloc {
					return reflect.Value{}, false
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(x)
	}
	return v, true
}
