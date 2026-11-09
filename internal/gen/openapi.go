package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/maulai/tether/internal/jsregex"
	"github.com/maulai/tether/internal/vtag"
)

type obj = map[string]any

// openAPI builds the OpenAPI 3.1 document. It describes the API for other
// tools; where JSON Schema cannot say exactly what tether checks (text that
// is only whitespace, conditions), it says it in a description.
type openAPI struct {
	m       *model
	schemas obj
}

func (m *model) renderOpenAPI(title, version string) []byte {
	o := &openAPI{m: m, schemas: obj{}}
	paths := obj{}
	for _, r := range m.routes {
		item, _ := paths[openAPIPath(r.path)].(obj)
		if item == nil {
			item = obj{}
			paths[openAPIPath(r.path)] = item
		}
		item[strings.ToLower(r.method)] = o.operation(r)
	}
	o.schemas["FieldError"] = obj{
		"type":     "object",
		"required": []string{"field", "message"},
		"properties": obj{
			"field":   obj{"type": "string", "description": "Path in the input, with dots and indices: items.2.sku."},
			"rule":    obj{"type": "string", "description": "The rule that failed; missing for errors written by a handler."},
			"message": obj{"type": "string"},
		},
	}
	props := obj{
		"type":     obj{"type": "string"},
		"title":    obj{"type": "string"},
		"status":   obj{"type": "integer"},
		"detail":   obj{"type": "string"},
		"instance": obj{"type": "string", "description": "Names a server error; the server's log has the same instance with the cause."},
		"errors":   obj{"type": "array", "items": ref("FieldError")},
	}
	if problems := m.sortedProblems(); len(problems) > 0 {
		var cases []any
		for _, pd := range problems {
			desc := pd.doc
			if desc == "" {
				desc = pd.message
			}
			cases = append(cases, obj{"const": pd.code, "description": strings.TrimSpace(fmt.Sprintf("Status %d. %s", pd.status, desc))})
		}
		props["code"] = obj{"type": "string", "description": "The case of a problem the client can react to.", "oneOf": cases}
	}
	o.schemas["Problem"] = obj{
		"type":        "object",
		"description": "RFC 9457 problem details.",
		"required":    []string{"type", "title", "status"},
		"properties":  props,
	}
	problem := func(desc string) obj {
		return obj{"description": desc, "content": obj{"application/problem+json": obj{"schema": ref("Problem")}}}
	}
	responses := obj{
		"BadRequest": problem("The request body could not be read."),
		"Invalid":    problem("The input failed validation; errors names the fields."),
		"Error":      problem("Any other error."),
	}
	components := obj{"schemas": o.schemas, "responses": responses}
	if m.anyAuth {
		responses["Unauthorized"] = problem("The request has no valid credentials.")
		responses["Forbidden"] = problem("The credentials do not allow this request.")
		schemes := obj{}
		for _, a := range m.sortedAuths() {
			var s obj
			switch {
			case a.cookie != "":
				s = obj{"type": "apiKey", "in": "cookie", "name": a.cookie}
			case a.header != "":
				s = obj{"type": "apiKey", "in": "header", "name": a.header}
			default:
				s = obj{"type": "http", "scheme": "bearer"}
			}
			if a.doc != "" {
				s["description"] = a.doc
			}
			schemes[a.name] = s
		}
		components["securitySchemes"] = schemes
	}
	doc := obj{
		"openapi":    "3.1.0",
		"info":       obj{"title": title, "version": version},
		"paths":      paths,
		"components": components,
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(doc)
	return b.Bytes()
}

func ref(name string) obj { return obj{"$ref": "#/components/schemas/" + name} }

// openAPIPath writes a ServeMux path the OpenAPI way: {name...} becomes
// {name}, and {$} disappears.
func openAPIPath(p string) string {
	p = strings.ReplaceAll(p, "{$}", "")
	for {
		i := strings.Index(p, "...}")
		if i < 0 {
			return p
		}
		p = p[:i] + p[i+3:]
	}
}

func (o *openAPI) operation(r *route) obj {
	op := obj{"operationId": r.name}
	if r.doc != "" {
		op["summary"] = firstSentence(r.doc)
		if op["summary"] != r.doc {
			op["description"] = r.doc
		}
	}
	var params []any
	for i, p := range r.params {
		params = append(params, obj{"name": p.name, "in": "path", "required": true, "schema": o.schema(o.m.pathTypes[r][i], nil)})
	}
	in := o.m.inTypes[r]
	switch {
	case r.upload:
		op["requestBody"] = obj{
			"required": true,
			"content": obj{"multipart/form-data": obj{"schema": obj{
				"type":       "object",
				"required":   []string{"file"},
				"properties": obj{"file": obj{"type": "string", "format": "binary"}},
			}}},
		}
	case in != nil && r.query:
		for _, f := range in.def.fields {
			p := obj{"name": f.name, "in": "query", "schema": o.field(in.def, f, nil)}
			if f.required() {
				p["required"] = true
			}
			if f.doc != "" {
				p["description"] = f.doc
			}
			params = append(params, p)
		}
	case in != nil:
		op["requestBody"] = obj{
			"required": hasRequired(in.def),
			"content":  obj{"application/json": obj{"schema": o.schema(in, nil)}},
		}
	}
	if len(params) > 0 {
		op["parameters"] = params
	}

	responses := obj{
		"422":     obj{"$ref": "#/components/responses/Invalid"},
		"default": obj{"$ref": "#/components/responses/Error"},
	}
	if in != nil && !r.query || r.upload {
		responses["400"] = obj{"$ref": "#/components/responses/BadRequest"}
	}
	if o.m.anyAuth {
		switch a := r.access; {
		case a.auth != nil:
			scopes := a.scopes
			if scopes == nil {
				scopes = []string{}
			}
			op["security"] = []any{obj{a.auth.name: scopes}}
			responses["401"] = obj{"$ref": "#/components/responses/Unauthorized"}
			responses["403"] = obj{"$ref": "#/components/responses/Forbidden"}
		default:
			op["security"] = []any{}
		}
	}
	status := "200"
	if r.method == "POST" {
		status = "201"
	}
	switch {
	case r.out == nil:
		responses["204"] = obj{"description": "No Content"}
	case r.file:
		responses["200"] = obj{"description": "A file", "content": obj{"application/octet-stream": obj{"schema": obj{"type": "string", "format": "binary"}}}}
	default:
		desc := "OK"
		if status == "201" {
			desc = "Created"
		}
		responses[status] = obj{"description": desc, "content": obj{"application/json": obj{"schema": o.schema(o.m.outType[r], nil)}}}
	}
	op["responses"] = responses
	return op
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// schema returns the JSON Schema of t. subst maps the type parameters of a
// generic struct to the arguments of the instance being written.
func (o *openAPI) schema(t *tsType, subst map[string]*tsType) obj {
	switch t.kind {
	case kString:
		s := obj{"type": "string"}
		if t.format != "" {
			s["format"] = t.format
			if js, err := jsregex.Translate(o.m.patterns[t.format].expr); err == nil {
				s["pattern"] = "^(?:" + js + ")$"
			}
		}
		return s
	case kInteger:
		s := obj{"type": "integer"}
		switch t.intKind {
		case "int32", "uint32":
			s["format"] = "int32"
		case "int64", "uint64", "int", "uint":
			s["format"] = "int64"
		}
		return s
	case kNumber:
		return obj{"type": "number"}
	case kBoolean:
		return obj{"type": "boolean"}
	case kDatetime:
		return obj{"type": "string", "format": "date-time"}
	case kBytes:
		return obj{"type": "string", "contentEncoding": "base64"}
	case kArray:
		return obj{"type": "array", "items": o.schema(t.elem, subst)}
	case kRecord:
		s := obj{"type": "object", "additionalProperties": o.schema(t.elem, subst)}
		if t.enum != nil {
			s["propertyNames"] = o.schema(&tsType{kind: kEnum, enum: t.enum}, subst)
		}
		return s
	case kNullable:
		return obj{"anyOf": []any{o.schema(t.elem, subst), obj{"type": "null"}}}
	case kEnum:
		name := t.enum.name
		if _, ok := o.schemas[name]; !ok {
			s := obj{"type": "string", "enum": t.enum.values}
			if t.enum.doc != "" {
				s["description"] = t.enum.doc
			}
			o.schemas[name] = s
		}
		return ref(name)
	case kParam:
		if a, ok := subst[t.param]; ok {
			return o.schema(a, nil)
		}
		return obj{}
	case kObject:
		return o.object(t.def, subst)
	case kRef:
		name := o.instanceName(t, subst)
		if _, ok := o.schemas[name]; !ok {
			o.schemas[name] = obj{} // reserve, for recursive types
			inner := map[string]*tsType{}
			for i, p := range t.def.params {
				if i < len(t.args) {
					inner[p] = resolve(t.args[i], subst)
				}
			}
			o.schemas[name] = o.object(t.def, inner)
		}
		return ref(name)
	}
	return obj{}
}

// resolve replaces type parameters in t by their arguments.
func resolve(t *tsType, subst map[string]*tsType) *tsType {
	if t.kind == kParam {
		if a, ok := subst[t.param]; ok {
			return a
		}
	}
	return t
}

// instanceName names the component of a struct; an instance of a generic
// struct gets its arguments appended, Page_Task.
func (o *openAPI) instanceName(t *tsType, subst map[string]*tsType) string {
	name := t.def.name
	for _, a := range t.args {
		name += "_" + o.argName(resolve(a, subst), subst)
	}
	return name
}

func (o *openAPI) argName(t *tsType, subst map[string]*tsType) string {
	switch t.kind {
	case kRef:
		return o.instanceName(t, subst)
	case kEnum:
		return t.enum.name
	case kArray:
		return o.argName(t.elem, subst) + "List"
	case kNullable:
		return o.argName(t.elem, subst)
	case kString, kDatetime, kBytes:
		return "String"
	case kInteger, kNumber:
		return "Number"
	case kBoolean:
		return "Boolean"
	}
	return "Value"
}

func (o *openAPI) object(d *structDef, subst map[string]*tsType) obj {
	props := obj{}
	var required []string
	for _, f := range d.fields {
		props[f.name] = o.field(d, f, subst)
		if !optional(d, f) {
			required = append(required, f.name)
		}
	}
	s := obj{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	if d.in {
		// The server rejects unknown fields in its input.
		s["additionalProperties"] = false
	}
	if d.doc != "" {
		s["description"] = d.doc
	}
	return s
}

func (o *openAPI) field(d *structDef, f *fieldDef, subst map[string]*tsType) obj {
	s := o.schema(f.typ, subst)
	var notes []string
	if f.doc != "" {
		notes = append(notes, f.doc)
	}
	if d.in {
		target := s
		if f.typ.kind == kNullable {
			target = s["anyOf"].([]any)[0].(obj)
		}
		notes = append(notes, o.rules(target, f.tag.Rules, f.typ.base())...)
		if f.tag.Elem != nil {
			o.elemRules(target, *f.tag.Elem, f.typ.base())
		}
	}
	if len(notes) > 0 {
		s = withKey(s, "description", strings.Join(notes, " "))
	}
	if f.example != "" {
		var v any = f.example
		if b := f.typ.base().kind; b != kString && b != kDatetime && b != kEnum && b != kBytes {
			if err := json.Unmarshal([]byte(f.example), &v); err != nil {
				v = f.example
			}
		}
		s = withKey(s, "examples", []any{v})
	}
	return s
}

// withKey adds a key to a schema. A $ref keeps its siblings in OpenAPI 3.1,
// so a reference can carry a description of its own.
func withKey(s obj, k string, v any) obj {
	out := obj{}
	for kk, vv := range s {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func (o *openAPI) elemRules(s obj, tag vtag.Tag, t *tsType) {
	var inner obj
	switch t.kind {
	case kArray:
		inner, _ = s["items"].(obj)
	case kRecord:
		inner, _ = s["additionalProperties"].(obj)
	}
	if inner == nil {
		return
	}
	notes := o.rules(inner, tag.Rules, t.elem.base())
	if len(notes) > 0 {
		inner["description"] = strings.Join(notes, " ")
	}
	if tag.Elem != nil {
		o.elemRules(inner, *tag.Elem, t.elem.base())
	}
}

// rules adds the JSON Schema keywords for a tag's rules to s and returns
// sentences for what has no keyword.
func (o *openAPI) rules(s obj, rules []vtag.Rule, t *tsType) []string {
	var notes []string
	text := t.kind == kString || t.kind == kEnum || t.kind == kBytes
	list := t.kind == kArray || t.kind == kRecord
	for _, r := range rules {
		n, _ := strconv.ParseFloat(r.Arg, 64)
		switch r.Name {
		case "required":
			switch {
			case text:
				s["minLength"] = 1
				s["pattern"] = `\S`
			case list && t.kind == kArray:
				s["minItems"] = 1
			case list:
				s["minProperties"] = 1
			}
		case "requiredif", "forbiddenif":
			field, value, negate, _ := vtag.ParseCond(r.Arg)
			notes = append(notes, condSentence(r.Name, field, value, negate))
		case "min", "max":
			kw := map[bool]string{true: "minimum", false: "maximum"}[r.Name == "min"]
			switch {
			case text:
				kw = map[bool]string{true: "minLength", false: "maxLength"}[r.Name == "min"]
			case t.kind == kArray:
				kw = map[bool]string{true: "minItems", false: "maxItems"}[r.Name == "min"]
			case t.kind == kRecord:
				kw = map[bool]string{true: "minProperties", false: "maxProperties"}[r.Name == "min"]
			}
			s[kw] = number(n)
		case "gt":
			s["exclusiveMinimum"] = number(n)
		case "lt":
			s["exclusiveMaximum"] = number(n)
		case "email":
			s["format"] = "email"
		case "url":
			s["format"] = "uri"
		case "oneof":
			s["enum"] = strings.Fields(r.Arg)
		default:
			if pt, ok := o.m.patterns[r.Name]; ok {
				if js, err := jsregex.Translate(pt.expr); err == nil {
					s["pattern"] = "^(?:" + js + ")$"
				}
			} else {
				notes = append(notes, "The server also checks it with the rule "+r.Name+".")
			}
		}
	}
	return notes
}

func number(f float64) any {
	if f == float64(int64(f)) {
		return int64(f)
	}
	return f
}

func condSentence(rule, field, value string, negate bool) string {
	var when string
	switch {
	case value == "" && !negate:
		when = field + " is empty"
	case value == "":
		when = field + " is set"
	case negate:
		when = field + " is not " + value
	default:
		when = field + " is " + value
	}
	if rule == "requiredif" {
		return "Required when " + when + "."
	}
	return "Must be empty when " + when + "."
}
