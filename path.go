package tether

import (
	"encoding"
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

// PathValue parses the path parameter name into a T: a string type, an enum,
// a number, a boolean, a time.Time, or a type with UnmarshalText such as
// uuid.UUID. A value that does not parse, or is not a value of the enum,
// returns an error that answers the request with 404: a malformed address
// names no resource. Return it from the handler as it is.
//
//	id, err := tether.PathValue[uuid.UUID](r, "id")
//	if err != nil {
//		return Task{}, err
//	}
//
// It panics for a T it cannot parse into and for a name the route's pattern
// does not have: both are programming errors.
func PathValue[T any](r *http.Request, name string) (T, error) {
	var v T
	if r.Pattern != "" && !strings.Contains(r.Pattern, "{"+name+"}") && !strings.Contains(r.Pattern, "{"+name+"...}") {
		panic(fmt.Sprintf("tether: PathValue: pattern %q has no parameter {%s}", r.Pattern, name))
	}
	s := r.PathValue(name)
	rv := reflect.ValueOf(&v).Elem()
	if !parsePath(s, rv) {
		return v, Fail(http.StatusNotFound, message("notFound", nil))
	}
	return v, nil
}

func parsePath(s string, v reflect.Value) bool {
	t := v.Type()
	if u, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok && t != timeType {
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
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return s != "" && setNumber(v, s) == ""
	}
	panic(fmt.Sprintf("tether: PathValue cannot parse into %s", t))
}
