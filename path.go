package tether

import (
	"encoding"
	"reflect"
	"strings"
)

// pathParam is one parameter of a route's pattern, which the handler takes
// as an argument after r.
type pathParam struct {
	name string
	rest bool // {name...}, the rest of the path
	typ  reflect.Type
}

// patternParams lists the parameters of a ServeMux path in order; {$}
// marks the end of the path and is none.
func patternParams(path string) []pathParam {
	var out []pathParam
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			return out
		}
		j := strings.Index(path[i:], "}")
		if j < 0 {
			return out
		}
		name := path[i+1 : i+j]
		path = path[i+j+1:]
		if name == "$" {
			continue
		}
		out = append(out, pathParam{name: strings.TrimSuffix(name, "..."), rest: strings.HasSuffix(name, "...")})
	}
}

// pathType reports whether a path parameter can be parsed into t.
func pathType(t reflect.Type) bool {
	if t == timeType || reflect.PointerTo(t).Implements(textUnmarshaler) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// parsePath parses one path value into v, whose type pathType accepted. A
// value of an enum must be one of its values, a value of a type with a
// Format must match it.
func parsePath(s string, v reflect.Value) bool {
	t := v.Type()
	if u, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok && t != timeType {
		if f, ok := formatFor(t); ok && !f.re.MatchString(s) {
			return false
		}
		return u.UnmarshalText([]byte(s)) == nil
	}
	switch {
	case t == timeType:
		tm, err := parseTime(s)
		if err != nil {
			return false
		}
		v.Set(reflect.ValueOf(tm))
		return true
	case t.Kind() == reflect.String:
		if set := enumValues(t); set != nil && !set[s] {
			return false
		}
		v.SetString(s)
		return true
	case t.Kind() == reflect.Bool:
		switch s {
		case "true":
			v.SetBool(true)
		case "false":
		default:
			return false
		}
		return true
	}
	return s != "" && setNumber(v, s) == ""
}
