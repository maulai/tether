package gen

import (
	"fmt"
	"strings"
)

// renderIndex writes index.ts: types, enum values, validators and one
// function per route.
func (m *model) renderIndex(header string) []byte {
	var b strings.Builder
	b.WriteString(header + "\n\n")

	used := map[string]bool{}
	var body strings.Builder

	enums := m.sortedEnums()
	if len(enums) > 0 {
		body.WriteString("// ── Enums ─────────────────────────────────────────────────────────────────\n\n")
	}
	for _, e := range enums {
		writeDoc(&body, "", e.doc)
		quoted := make([]string, 0, len(e.values))
		for _, v := range e.listed() {
			quoted = append(quoted, tsString(v))
		}
		fmt.Fprintf(&body, "export const %s = [%s] as const;\n", valuesName(e), strings.Join(quoted, ", "))
		if e.hasEmpty() {
			body.WriteString("// \"\" is a declared value that means \"not set\"; it is valid but not offered.\n")
			fmt.Fprintf(&body, "export type %s = (typeof %s)[number] | \"\";\n\n", e.name, valuesName(e))
		} else {
			fmt.Fprintf(&body, "export type %s = (typeof %s)[number];\n\n", e.name, valuesName(e))
		}
	}

	defs := m.sortedDefs()
	if len(defs) > 0 {
		body.WriteString("// ── Types ─────────────────────────────────────────────────────────────────\n\n")
	}
	for _, d := range defs {
		writeDoc(&body, "", d.doc)
		params := ""
		if len(d.params) > 0 {
			params = "<" + strings.Join(d.params, ", ") + ">"
		}
		fmt.Fprintf(&body, "export interface %s%s %s\n\n", d.name, params, m.objectType(d, ""))
	}

	var schemas []*structDef
	for _, d := range defs {
		if d.in && len(d.params) == 0 {
			schemas = append(schemas, d)
		}
	}
	if len(schemas) > 0 {
		used["schema"], used["Schema"] = true, true
		body.WriteString("// ── Validators ────────────────────────────────────────────────────────────\n\n")
	}
	for _, d := range schemas {
		fmt.Fprintf(&body, "export const %s: Schema<%s> = schema<%s>(%s);\n\n", schemaName(d), d.name, d.name, m.fieldSpecs(d, ""))
	}

	if len(m.routes) > 0 {
		body.WriteString("// ── Routes ────────────────────────────────────────────────────────────────\n\n")
	}
	for _, r := range m.routes {
		m.renderRoute(&body, r, used)
	}

	var imports []string
	for _, name := range []string{"call", "schema", "url"} {
		if used[name] {
			imports = append(imports, name)
		}
	}
	for _, name := range []string{"RequestOptions", "Schema"} {
		if used[name] {
			imports = append(imports, "type "+name)
		}
	}
	if len(imports) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./runtime\";\n\n", strings.Join(imports, ", "))
	}
	b.WriteString(strings.TrimRight(body.String(), "\n") + "\n")
	return []byte(b.String())
}

func writeDoc(b *strings.Builder, indent, doc string) {
	if doc == "" {
		return
	}
	lines := strings.Split(doc, "\n")
	if len(lines) == 1 {
		fmt.Fprintf(b, "%s/** %s */\n", indent, escapeComment(doc))
		return
	}
	fmt.Fprintf(b, "%s/**\n", indent)
	for _, l := range lines {
		if l == "" {
			fmt.Fprintf(b, "%s *\n", indent)
		} else {
			fmt.Fprintf(b, "%s * %s\n", indent, escapeComment(l))
		}
	}
	fmt.Fprintf(b, "%s */\n", indent)
}

func escapeComment(s string) string { return strings.ReplaceAll(s, "*/", "*\\/") }

// optional reports whether a field may be missing in TypeScript. An input
// field is optional unless it is required. A PUT replaces the whole record,
// so a struct it reads must be sent with every field; like an output field,
// it may be missing only with omitempty or omitzero.
func optional(d *structDef, f *fieldDef) bool {
	if d.in && !d.whole {
		return !f.required()
	}
	return f.omitEmpty
}

func (m *model) objectType(d *structDef, indent string) string {
	if len(d.fields) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")
	for _, f := range d.fields {
		writeDoc(&b, indent+"  ", f.doc)
		q := ""
		if optional(d, f) {
			q = "?"
		}
		fmt.Fprintf(&b, "%s  %s%s: %s;\n", indent, tsKey(f.name), q, m.expr(f.typ, indent+"  "))
	}
	b.WriteString(indent + "}")
	return b.String()
}

// expr writes a type as a TypeScript type expression.
func (m *model) expr(t *tsType, indent string) string {
	switch t.kind {
	case kString, kDatetime, kBytes:
		return "string"
	case kNumber, kInteger:
		return "number"
	case kBoolean:
		return "boolean"
	case kUnknown:
		return "unknown"
	case kArray:
		el := m.expr(t.elem, indent)
		if strings.Contains(el, "|") {
			el = "(" + el + ")"
		}
		return el + "[]"
	case kRecord:
		if t.enum != nil {
			return "Partial<Record<" + t.enum.name + ", " + m.expr(t.elem, indent) + ">>"
		}
		return "Record<string, " + m.expr(t.elem, indent) + ">"
	case kNullable:
		return m.expr(t.elem, indent) + " | null"
	case kRef:
		if len(t.args) == 0 {
			return t.def.name
		}
		args := make([]string, len(t.args))
		for i, a := range t.args {
			args[i] = m.expr(a, indent)
		}
		return t.def.name + "<" + strings.Join(args, ", ") + ">"
	case kObject:
		return m.objectType(t.def, indent)
	case kEnum:
		return t.enum.name
	case kParam:
		return t.param
	}
	return "unknown"
}

func (m *model) fieldSpecs(d *structDef, indent string) string {
	var b strings.Builder
	b.WriteString("{\n")
	for _, f := range d.fields {
		spec := m.spec(f.typ, indent+"  ")
		if f.rawTag != "" {
			spec = strings.TrimSuffix(spec, " }") + ", v: " + tsString(f.rawTag) + " }"
		}
		fmt.Fprintf(&b, "%s  %s: %s,\n", indent, tsKey(f.name), spec)
	}
	b.WriteString(indent + "}")
	return b.String()
}

// spec writes the FieldSpec of a type for the runtime's validator.
func (m *model) spec(t *tsType, indent string) string {
	switch t.kind {
	case kNullable:
		inner := m.spec(t.elem, indent)
		return strings.TrimSuffix(inner, " }") + ", nullable: true }"
	case kString:
		return `{ type: "string" }`
	case kEnum:
		return `{ type: "string", enum: ` + enumList(t.enum) + " }"
	case kBytes:
		return `{ type: "bytes" }`
	case kInteger:
		return `{ type: "number", int: "` + t.intKind + `" }`
	case kNumber:
		return `{ type: "number" }`
	case kBoolean:
		return `{ type: "boolean" }`
	case kDatetime:
		return `{ type: "datetime" }`
	case kArray:
		return `{ type: "array", elem: ` + m.spec(t.elem, indent) + " }"
	case kRecord:
		if t.enum != nil {
			return `{ type: "record", keys: ` + enumList(t.enum) + `, elem: ` + m.spec(t.elem, indent) + " }"
		}
		return `{ type: "record", elem: ` + m.spec(t.elem, indent) + " }"
	case kRef:
		return `{ type: "object", schema: () => ` + schemaName(t.def) + " }"
	case kObject:
		return `{ type: "object", fields: ` + m.fieldSpecs(t.def, indent) + " }"
	}
	return `{ type: "unknown" }`
}

func (m *model) renderRoute(b *strings.Builder, r *route, used map[string]bool) {
	used["call"], used["RequestOptions"] = true, true
	doc := r.doc
	if doc != "" {
		doc += "\n\n"
	}
	writeDoc(b, "", doc+r.method+" "+r.path)

	var params []string
	for _, p := range r.params {
		params = append(params, p.name+": string")
	}
	in := m.inTypes[r]
	var payload string
	switch {
	case r.upload:
		params = append(params, "file: Blob")
		payload = "{ file }"
	case in != nil:
		opt := ""
		if !hasRequired(in.def) {
			opt = "?"
		}
		params = append(params, "input"+opt+": "+m.expr(in, ""))
		strip := schemaName(in.def) + ".strip(input)"
		if opt != "" {
			strip = "input && " + strip
		}
		key := "body"
		if r.query {
			key = "query"
		}
		payload = "{ " + key + ": " + strip + " }"
	default:
		payload = "{}"
	}
	params = append(params, "options?: RequestOptions")

	result, ret := "none", "void"
	switch {
	case r.file:
		result, ret = "blob", "Blob"
	case r.out != nil:
		result, ret = "json", m.expr(m.outType[r], "")
	}
	path := m.pathTemplate(r)
	fmt.Fprintf(b, "export function %s(%s): Promise<%s> {\n", r.name, strings.Join(params, ", "), ret)
	fmt.Fprintf(b, "  return call<%s>(%s, %s, %s, options, %s);\n}\n\n", ret, tsString(r.method), path, payload, tsString(result))

	if r.file && r.method == "GET" {
		used["url"] = true
		var uparams []string
		for _, p := range r.params {
			uparams = append(uparams, p.name+": string")
		}
		query := ""
		if in != nil {
			opt := "?"
			if hasRequired(in.def) {
				opt = ""
			}
			uparams = append(uparams, "input"+opt+": "+m.expr(in, ""))
			query = ", input"
		}
		fmt.Fprintf(b, "/** The address of %s, for links. */\n", r.name)
		fmt.Fprintf(b, "export function %sUrl(%s): string {\n  return url(%s%s);\n}\n\n", r.name, strings.Join(uparams, ", "), path, query)
	}
}

// enumList is the expression for all valid values of an enum, "" included
// when Go declares it.
func enumList(e *enumDef) string {
	if e.hasEmpty() {
		return "[..." + valuesName(e) + `, ""]`
	}
	return valuesName(e)
}

// hasRequired reports whether an input must be passed: whether it has a
// field that is not optional.
func hasRequired(d *structDef) bool {
	for _, f := range d.fields {
		if !optional(d, f) {
			return true
		}
	}
	return false
}

// pathTemplate writes the path as a template literal with each parameter
// encoded. A rest parameter {name...} keeps its slashes.
func (m *model) pathTemplate(r *route) string {
	var b strings.Builder
	b.WriteString("`")
	path := r.path
	for {
		i := strings.Index(path, "{")
		if i < 0 {
			b.WriteString(escapeTemplate(path))
			break
		}
		j := strings.Index(path[i:], "}")
		b.WriteString(escapeTemplate(path[:i]))
		name := path[i+1 : i+j]
		path = path[i+j+1:]
		switch {
		case name == "$":
		case strings.HasSuffix(name, "..."):
			n := strings.TrimSuffix(name, "...")
			fmt.Fprintf(&b, "${%s.split(\"/\").map(encodeURIComponent).join(\"/\")}", n)
		default:
			fmt.Fprintf(&b, "${encodeURIComponent(%s)}", name)
		}
	}
	b.WriteString("`")
	return b.String()
}

func escapeTemplate(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "`", "\\`")
	return strings.ReplaceAll(s, "${", "\\${")
}
