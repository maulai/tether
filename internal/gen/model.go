package gen

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/maulai/tether/internal/vtag"
)

// tsType is a Go type as both outputs see it.
type tsType struct {
	kind    kind
	elem    *tsType    // array, record, nullable
	def     *structDef // ref (named struct) and object (anonymous struct)
	args    []*tsType  // type arguments of a generic ref
	enum    *enumDef   // enum, and the key type of a record with enum keys
	param   string     // type parameter name
	intKind string     // integer: the Go type, for range checks
}

type kind int

const (
	kString kind = iota
	kNumber
	kInteger
	kBoolean
	kDatetime
	kBytes
	kUnknown
	kArray
	kRecord
	kNullable
	kRef
	kObject
	kEnum
	kParam
)

type structDef struct {
	name   string
	obj    *types.TypeName // nil for an anonymous struct
	params []string
	fields []*fieldDef
	doc    string
	in     bool // reached from a handler's input
	out    bool // reached from a handler's output
}

type fieldDef struct {
	name      string
	typ       *tsType
	doc       string
	example   string
	rawTag    string
	tag       vtag.Tag
	omitEmpty bool
	asString  bool
	pos       token.Pos
}

func (f *fieldDef) required() bool { return f.tag.Has("required") }

type enumDef struct {
	name   string
	obj    *types.TypeName
	consts []*types.Const
	values []string // in source order, "" included when declared
	doc    string
	used   bool
}

func (e *enumDef) hasEmpty() bool {
	for _, v := range e.values {
		if v == "" {
			return true
		}
	}
	return false
}

// listed are the values offered for selection: without "", which a select
// input cannot show.
func (e *enumDef) listed() []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range e.values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

type model struct {
	*program
	pos     token.Pos // where the type being mapped is used, for errors
	defs    map[*types.TypeName]*structDef
	enums   map[*types.TypeName]*enumDef
	noEnum  map[*types.TypeName]bool
	names   map[string]token.Pos // TypeScript names in use, for collisions
	textM   *types.Interface
	jsonM   *types.Interface
	inTypes map[*route]*tsType
	outType map[*route]*tsType
	// registers lists, per handler package, the enums its handlers' inputs
	// use; tether_gen.go in that package registers them.
	registers map[*types.Package][]*enumDef
}

func newModel(p *program) *model {
	m := &model{
		program: p,
		defs:    map[*types.TypeName]*structDef{},
		enums:   map[*types.TypeName]*enumDef{},
		noEnum:  map[*types.TypeName]bool{},
		names:   map[string]token.Pos{},
		textM:   method("MarshalText"),
		jsonM:   method("MarshalJSON"),
		inTypes: map[*route]*tsType{},
		outType: map[*route]*tsType{},
	}
	for _, r := range p.routes {
		where := r.handler.Name()
		m.pos = r.handler.Pos()
		if r.in != nil && !r.upload {
			t := m.typ(r.in, where)
			m.inTypes[r] = t
			m.mark(t, true, map[*structDef]bool{})
		}
		if r.out != nil && !r.file {
			t := m.typ(r.out, where)
			m.outType[r] = t
			m.mark(t, false, map[*structDef]bool{})
		}
	}
	m.checkInputs()
	m.checkNames()
	m.planRegistrations()
	return m
}

// planRegistrations decides where enums are registered: in the package of
// the handler that takes them. Route refers to the handler, so Go links that
// package, and runs its init, wherever the route is built, in tests too.
// Enum packages are never written to, so a domain package does not depend on
// tether and an enum may live in another module or in generated code. No
// import cycle can arise: the handler package already imports the enum's
// package, directly or through the types of its input.
func (m *model) planRegistrations() {
	m.registers = map[*types.Package][]*enumDef{}
	need := map[*types.Package]map[*enumDef]bool{}
	where := map[*types.Package]token.Pos{}
	for _, r := range m.routes {
		t := m.inTypes[r]
		if t == nil {
			continue
		}
		pkg := r.handler.Pkg()
		if need[pkg] == nil {
			need[pkg] = map[*enumDef]bool{}
			where[pkg] = r.handler.Pos()
		}
		collectEnums(t, need[pkg], map[*structDef]bool{})
	}
	for pkg, set := range need {
		if len(set) == 0 {
			continue
		}
		if m.roots[pkg] == nil {
			m.errorf(where[pkg], "handler package %s is outside the packages tether reads; tether writes the enum registration there, so add it to the package patterns", pkg.Path())
			continue
		}
		var list []*enumDef
		for e := range set {
			if e.obj.Pkg() != pkg {
				for _, c := range e.consts {
					if !c.Exported() {
						m.errorf(c.Pos(), "constant %s of enum %s is not exported, so package %s cannot register it; export it", c.Name(), e.name, pkg.Path())
					}
				}
			}
			list = append(list, e)
		}
		sort.Slice(list, func(i, j int) bool { return qualified(list[i].obj) < qualified(list[j].obj) })
		m.registers[pkg] = list
	}
}

func collectEnums(t *tsType, set map[*enumDef]bool, seen map[*structDef]bool) {
	if t == nil {
		return
	}
	if t.enum != nil {
		set[t.enum] = true // an enum, or the keys of a record
	}
	collectEnums(t.elem, set, seen)
	for _, a := range t.args {
		collectEnums(a, set, seen)
	}
	if t.def != nil && !seen[t.def] {
		seen[t.def] = true
		for _, f := range t.def.fields {
			collectEnums(f.typ, set, seen)
		}
	}
}

// errorf reports at pos, or where the current type is used when the error
// has no position of its own.
func (m *model) errorf(pos token.Pos, format string, args ...any) {
	if !pos.IsValid() {
		pos = m.pos
	}
	m.program.errorf(pos, format, args...)
}

// method builds the interface with one method name() ([]byte, error).
func method(name string) *types.Interface {
	bytes := types.NewSlice(types.Typ[types.Byte])
	errType := types.Universe.Lookup("error").Type()
	sig := types.NewSignatureType(nil, nil, nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, nil, "", bytes), types.NewVar(token.NoPos, nil, "", errType)), false)
	iface := types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, name, sig)}, nil)
	iface.Complete()
	return iface
}

func (m *model) implements(t types.Type, iface *types.Interface) bool {
	return types.Implements(t, iface) || types.Implements(types.NewPointer(t), iface)
}

func (m *model) typ(t types.Type, at string) *tsType {
	t = types.Unalias(t)
	switch x := t.(type) {
	case *types.Pointer:
		inner := m.typ(x.Elem(), at)
		if inner.kind == kNullable {
			m.errorf(token.NoPos, "%s: pointers to pointers are not supported", at)
		}
		return &tsType{kind: kNullable, elem: inner}
	case *types.Basic:
		switch {
		case x.Kind() == types.String:
			return &tsType{kind: kString}
		case x.Kind() == types.Bool:
			return &tsType{kind: kBoolean}
		case x.Info()&types.IsInteger != 0:
			return &tsType{kind: kInteger, intKind: intKind(x)}
		case x.Info()&types.IsFloat != 0:
			return &tsType{kind: kNumber}
		}
	case *types.Named:
		obj := x.Obj()
		path := ""
		if obj.Pkg() != nil {
			path = obj.Pkg().Path()
		}
		switch {
		case path == "time" && obj.Name() == "Time":
			return &tsType{kind: kDatetime}
		case path == "encoding/json" && obj.Name() == "RawMessage":
			return &tsType{kind: kUnknown}
		case path == tetherPath && (obj.Name() == "File" || obj.Name() == "Upload"):
			m.errorf(obj.Pos(), "%s: tether.%s can only be the whole input or output of a handler", at, obj.Name())
			return &tsType{kind: kUnknown}
		case m.implements(x, m.textM):
			return &tsType{kind: kString}
		case m.implements(x, m.jsonM):
			m.errorf(token.NoPos, "%s: %s has its own MarshalJSON, so tether cannot know its JSON shape; use a plain type in the API", at, x)
			return &tsType{kind: kUnknown}
		}
		switch u := x.Underlying().(type) {
		case *types.Struct:
			def := m.structDef(x.Origin(), at)
			ref := &tsType{kind: kRef, def: def}
			for arg := range x.TypeArgs().Types() {
				ref.args = append(ref.args, m.typ(arg, at))
			}
			return ref
		case *types.Basic:
			if u.Kind() == types.String {
				if e := m.enumFor(x); e != nil {
					return &tsType{kind: kEnum, enum: e}
				}
			}
		}
		return m.typ(x.Underlying(), at)
	case *types.Slice:
		if b, ok := types.Unalias(x.Elem()).(*types.Basic); ok && b.Kind() == types.Byte {
			return &tsType{kind: kBytes}
		}
		return &tsType{kind: kArray, elem: m.typ(x.Elem(), at)}
	case *types.Array:
		return &tsType{kind: kArray, elem: m.typ(x.Elem(), at)}
	case *types.Map:
		if b, ok := x.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			m.errorf(token.NoPos, "%s: map keys must be strings, not %s", at, x.Key())
		}
		rec := &tsType{kind: kRecord, elem: m.typ(x.Elem(), at)}
		if k := m.typ(x.Key(), at); k.kind == kEnum {
			rec.enum = k.enum
		}
		return rec
	case *types.Interface:
		if x.Empty() {
			return &tsType{kind: kUnknown}
		}
	case *types.Struct:
		return &tsType{kind: kObject, def: m.fields(&structDef{}, x, at)}
	case *types.TypeParam:
		return &tsType{kind: kParam, param: x.Obj().Name()}
	}
	m.errorf(token.NoPos, "%s: type %s cannot be sent as JSON", at, t)
	return &tsType{kind: kUnknown}
}

func intKind(b *types.Basic) string {
	switch b.Kind() {
	case types.Int8, types.Int16, types.Int32, types.Int64, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Int, types.Uint:
		return b.Name()
	case types.Uintptr:
		return "uint64"
	}
	return "int"
}

func (m *model) structDef(obj *types.Named, at string) *structDef {
	tn := obj.Obj()
	if d, ok := m.defs[tn]; ok {
		return d
	}
	d := &structDef{name: tn.Name(), obj: tn, doc: m.typeDocs[tn]}
	m.defs[tn] = d // before the fields, for recursive types
	for tp := range obj.TypeParams().TypeParams() {
		d.params = append(d.params, tp.Obj().Name())
	}
	return m.fields(d, obj.Underlying().(*types.Struct), tn.Name())
}

// fields fills d with the fields of st as encoding/json writes them; see
// jsonFields in the tether package, which this mirrors.
func (m *model) fields(d *structDef, st *types.Struct, at string) *structDef {
	type candidate struct {
		v       *types.Var
		name    string
		tag     reflect.StructTag
		opts    string
		depth   int
		tagged  bool
		index   []int
		ownerAt string
	}
	var all []candidate
	var walk func(st *types.Struct, index []int, depth int, seen map[*types.Struct]bool)
	walk = func(st *types.Struct, index []int, depth int, seen map[*types.Struct]bool) {
		if seen[st] {
			return
		}
		seen[st] = true
		defer delete(seen, st)
		for i := range st.NumFields() {
			v := st.Field(i)
			tag := reflect.StructTag(st.Tag(i))
			jt := tag.Get("json")
			if jt == "-" {
				continue
			}
			name, opts, _ := strings.Cut(jt, ",")
			idx := append(append([]int(nil), index...), i)
			if v.Embedded() && name == "" {
				ft := types.Unalias(v.Type())
				ptr := false
				if p, ok := ft.(*types.Pointer); ok {
					ft, ptr = types.Unalias(p.Elem()), true
				}
				if inner, ok := ft.Underlying().(*types.Struct); ok && !m.implements(ft, m.jsonM) && !m.implements(ft, m.textM) {
					if !v.Exported() && ptr {
						continue
					}
					walk(inner, idx, depth+1, seen)
					continue
				}
			}
			if !v.Exported() {
				continue
			}
			c := candidate{v: v, name: name, tag: tag, opts: opts, depth: depth, tagged: name != "", index: idx}
			if name == "" {
				c.name = v.Name()
			}
			all = append(all, c)
		}
	}
	walk(st, nil, 0, map[*types.Struct]bool{})

	byName := map[string][]candidate{}
	var order []string
	for _, c := range all {
		if _, ok := byName[c.name]; !ok {
			order = append(order, c.name)
		}
		byName[c.name] = append(byName[c.name], c)
	}
	var chosen []candidate
	for _, name := range order {
		cs := byName[name]
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].depth < cs[j].depth })
		top := []candidate{cs[0]}
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
				m.errorf(top[0].v.Pos(), "%s: JSON name %q is ambiguous between embedded fields", at, name)
				continue
			}
			top = tagged
		}
		chosen = append(chosen, top[0])
	}
	sort.SliceStable(chosen, func(i, j int) bool { return lessIndex(chosen[i].index, chosen[j].index) })

	for _, c := range chosen {
		f := &fieldDef{name: c.name, rawTag: c.tag.Get("v"), example: c.tag.Get("example"), pos: c.v.Pos()}
		f.doc = m.varDocs[c.v.Origin()]
		for _, o := range strings.Split(c.opts, ",") {
			switch o {
			case "omitempty", "omitzero":
				f.omitEmpty = true
			case "string":
				f.asString = true
			}
		}
		tag, err := vtag.Parse(f.rawTag)
		if err != nil {
			m.errorf(c.v.Pos(), "%s.%s: %v", at, f.name, err)
		}
		f.tag = tag
		m.pos = c.v.Pos()
		f.typ = m.typ(c.v.Type(), at+"."+f.name)
		if f.asString {
			if k := f.typ.base().kind; k == kInteger || k == kNumber || k == kBoolean {
				f.typ = &tsType{kind: kString}
			}
		}
		d.fields = append(d.fields, f)
	}
	return d
}

func lessIndex(a, b []int) bool {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// base is the type without its pointer.
func (t *tsType) base() *tsType {
	if t.kind == kNullable {
		return t.elem
	}
	return t
}

// enumFor returns the enum of a named string type, or nil when its package
// declares no constant of it: then it is a plain string.
func (m *model) enumFor(named *types.Named) *enumDef {
	tn := named.Obj()
	if e, ok := m.enums[tn]; ok {
		return e
	}
	if m.noEnum[tn] || tn.Pkg() == nil {
		return nil
	}
	scope := tn.Pkg().Scope()
	var consts []*types.Const
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok && types.Identical(c.Type(), named) {
			consts = append(consts, c)
		}
	}
	if len(consts) == 0 {
		m.noEnum[tn] = true
		return nil
	}
	sort.Slice(consts, func(i, j int) bool { return consts[i].Pos() < consts[j].Pos() })
	e := &enumDef{name: tn.Name(), obj: tn, consts: consts, doc: m.typeDocs[tn]}
	for _, c := range consts {
		e.values = append(e.values, constant.StringVal(c.Val()))
	}
	m.enums[tn] = e
	return e
}

// mark records whether a struct is reached from an input or an output. An
// input field is optional unless it is required; an output field is
// optional only with omitempty. A struct used both ways follows the input
// rule, which is the looser one.
func (m *model) mark(t *tsType, in bool, seen map[*structDef]bool) {
	if t == nil {
		return
	}
	if t.enum != nil {
		t.enum.used = true // an enum, or the keys of a record
	}
	m.mark(t.elem, in, seen)
	for _, a := range t.args {
		m.mark(a, in, seen)
	}
	if t.def != nil && !seen[t.def] {
		seen[t.def] = true
		if in {
			t.def.in = true
		} else {
			t.def.out = true
		}
		for _, f := range t.def.fields {
			m.mark(f.typ, in, seen)
		}
	}
}

// checkInputs applies the same checks to input types that tether.Route
// applies at start-up, so a mistake fails the generator, not the server.
func (m *model) checkInputs() {
	defs := m.sortedDefs()
	for _, r := range m.routes {
		if t := m.inTypes[r]; t != nil {
			if t.kind != kRef {
				m.errorf(r.handler.Pos(), "%s: the input must be a named struct type", r.handler.Name())
				continue
			}
			if r.query {
				for _, f := range t.def.fields {
					if !queryable(f.typ) {
						m.errorf(f.pos, "%s.%s: a query parameter must be a scalar or a list of scalars", t.def.name, f.name)
					}
				}
			}
		}
	}
	for _, d := range defs {
		if !d.in {
			continue
		}
		if len(d.params) > 0 {
			m.errorf(d.obj.Pos(), "%s: input types must not be generic", d.name)
			continue
		}
		m.checkDef(d)
	}
}

func (m *model) checkDef(d *structDef) {
	byName := map[string]*fieldDef{}
	for _, f := range d.fields {
		byName[f.name] = f
	}
	for _, f := range d.fields {
		at := d.name + "." + f.name
		for _, inline := range inlineDefs(f.typ) {
			m.checkDef(inline)
		}
		if f.asString && len(f.tag.Rules) > 0 && !(len(f.tag.Rules) == 1 && f.tag.Rules[0].Name == "required") {
			m.errorf(f.pos, "%s: rules other than required are not supported with the json string option", at)
		}
		m.checkTag(at, f.tag, f.typ, f.pos)
		for _, r := range f.tag.Rules {
			if r.Name != "requiredif" && r.Name != "forbiddenif" {
				continue
			}
			field, value, _, err := vtag.ParseCond(r.Arg)
			if err != nil {
				continue
			}
			sib, ok := byName[field]
			if !ok {
				m.errorf(f.pos, "%s: %s refers to unknown field %q", at, r.Name, field)
				continue
			}
			if value == "" {
				continue
			}
			switch k, _ := vkind(sib.typ); {
			case k == vtag.Bool:
				if value != "true" && value != "false" {
					m.errorf(f.pos, "%s: %s compares boolean %s with %q; use true, false or nothing", at, r.Name, field, value)
				}
			case k.Number():
				if _, err := strconv.ParseFloat(value, 64); err != nil {
					m.errorf(f.pos, "%s: %s compares number %s with %q", at, r.Name, field, value)
				}
			case k != vtag.String:
				m.errorf(f.pos, "%s: %s can compare %s only with emptiness", at, r.Name, field)
			}
		}
	}
}

func (m *model) checkTag(at string, tag vtag.Tag, t *tsType, pos token.Pos) {
	k, ptr := vkind(t)
	for _, r := range tag.Rules {
		if err := vtag.Check(r, vtag.Field{Kind: k, Pointer: ptr, Project: m.projectRule}); err != nil {
			m.errorf(pos, "%s: %v", at, err)
		}
	}
	if tag.Elem != nil {
		b := t.base()
		if b.kind != kArray && b.kind != kRecord {
			m.errorf(pos, "%s: dive applies to slices and maps only", at)
			return
		}
		m.checkTag(at+" (elements)", *tag.Elem, b.elem, pos)
	}
}

// inlineDefs lists the anonymous structs inside t, which have no name of
// their own and are checked with the field that holds them.
func inlineDefs(t *tsType) []*structDef {
	if t == nil {
		return nil
	}
	if t.kind == kObject {
		return []*structDef{t.def}
	}
	return inlineDefs(t.elem)
}

// vkind reduces a type to the rule kinds of vtag.
func vkind(t *tsType) (vtag.Kind, bool) {
	ptr := t.kind == kNullable
	switch t.base().kind {
	case kString, kEnum, kBytes:
		return vtag.String, ptr
	case kInteger:
		if strings.HasPrefix(t.base().intKind, "u") {
			return vtag.Uint, ptr
		}
		return vtag.Int, ptr
	case kNumber:
		return vtag.Float, ptr
	case kBoolean:
		return vtag.Bool, ptr
	case kDatetime:
		return vtag.Time, ptr
	case kArray:
		return vtag.Slice, ptr
	case kRecord:
		return vtag.Map, ptr
	case kRef, kObject:
		return vtag.Struct, ptr
	case kUnknown:
		return vtag.Any, ptr
	}
	return vtag.Other, ptr
}

func queryable(t *tsType) bool {
	scalar := func(t *tsType) bool {
		switch t.kind {
		case kString, kEnum, kInteger, kNumber, kBoolean, kDatetime:
			return true
		}
		return false
	}
	b := t.base()
	if b.kind == kArray {
		return scalar(b.elem)
	}
	return scalar(b)
}

func (m *model) sortedDefs() []*structDef {
	var out []*structDef
	for _, d := range m.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func (m *model) sortedEnums() []*enumDef {
	var out []*enumDef
	for _, e := range m.enums {
		if e.used {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// checkNames makes sure every name in the TypeScript output is unique and
// usable: two Go types or handlers with the same name would collide.
func (m *model) checkNames() {
	seen := map[string]string{}
	claim := func(name, what string, pos token.Pos) {
		if reserved[name] {
			m.errorf(pos, "%s: %q is a reserved word in TypeScript; rename it", what, name)
			return
		}
		if prev, ok := seen[name]; ok {
			m.errorf(pos, "%s and %s both become %q in TypeScript; rename one", prev, what, name)
			return
		}
		seen[name] = what
	}
	for _, d := range m.sortedDefs() {
		claim(d.name, qualified(d.obj), d.obj.Pos())
		if d.in && len(d.params) == 0 {
			claim(schemaName(d), qualified(d.obj)+" (validator)", d.obj.Pos())
		}
	}
	for _, e := range m.sortedEnums() {
		claim(e.name, qualified(e.obj), e.obj.Pos())
		claim(valuesName(e), qualified(e.obj)+" (values)", e.obj.Pos())
	}
	for _, r := range m.routes {
		what := fmt.Sprintf("handler %s", qualifiedFunc(r.handler))
		claim(r.name, what, r.handler.Pos())
		if r.file && r.method == "GET" {
			claim(r.name+"Url", what+" (address)", r.handler.Pos())
		}
		for _, pp := range r.params {
			if !isIdent(pp.name) || reserved[pp.name] || pp.name == "input" || pp.name == "options" || pp.name == "file" {
				m.errorf(r.handler.Pos(), "%s: path parameter {%s} cannot be a TypeScript parameter name; rename it", r.handler.Name(), pp.name)
			}
		}
	}
	patterns := map[string]bool{}
	for _, r := range m.routes {
		key := r.method + " " + r.path
		if patterns[key] {
			m.errorf(r.handler.Pos(), "pattern %q is registered twice", key)
		}
		patterns[key] = true
	}
}

func qualified(tn *types.TypeName) string {
	if tn.Pkg() == nil {
		return tn.Name()
	}
	return tn.Pkg().Path() + "." + tn.Name()
}

func qualifiedFunc(fn *types.Func) string {
	return fn.FullName()
}

func schemaName(d *structDef) string { return lowerCamel(d.name) + "Schema" }
func valuesName(e *enumDef) string   { return lowerCamel(e.name) + "Values" }

var reserved = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`break case catch class const continue debugger default delete do else enum
		export extends false finally for function if import in instanceof new null return super switch this
		throw true try typeof var void while with yield let static implements interface package private
		protected public await any boolean number string symbol unknown never object undefined`) {
		reserved[w] = true
	}
}
