// Package tether serves typed Go handlers over HTTP and validates their
// input with the rules in the v struct tag. The command
// github.com/maulai/tether/cmd/tether reads the same handlers and writes an
// OpenAPI document and a typed TypeScript client that validates with the
// same rules and texts.
//
//	type TaskInput struct {
//		Title string `json:"title" v:"required,max=120"`
//	}
//
//	// CreateTask adds a task.
//	func CreateTask(r *http.Request, in TaskInput) (Task, error) { … }
//
//	mux.Handle(tether.Route("POST /v1/tasks", CreateTask))
//	mux.Handle(tether.Route("GET /v1/tasks/{id}", GetTask)) // func GetTask(r *http.Request, id int) (Task, error)
package tether

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
)

// MaxBodyBytes limits a JSON request body. Uploads do not count against it;
// they are streamed to the handler.
var MaxBodyBytes int64 = 1 << 20

var requestType = reflect.TypeFor[*http.Request]()

const shape = "func(r *http.Request[, path parameters][, in In]) ([Out, ]error)"

// Route turns handler into an http.Handler for pattern, a net/http
// ServeMux pattern with a method such as "PUT /v1/tasks/{id}". The two
// results fit mux.Handle directly.
//
// handler is a named function that takes r, then one argument per
// parameter of the pattern, in order, then optionally its input, and
// returns an output and an error, or only an error:
//
//	func ListTasks(r *http.Request) ([]Task, error)                       // GET /v1/tasks
//	func CreateTask(r *http.Request, in TaskInput) (Task, error)          // POST /v1/tasks
//	func GetTask(r *http.Request, id int) (Task, error)                   // GET /v1/tasks/{id}
//	func UpdateTask(r *http.Request, id int, in TaskInput) (Task, error)  // PUT /v1/tasks/{id}
//	func DeleteTask(r *http.Request, id int) error                        // DELETE /v1/tasks/{id}
//
// A path parameter is a string, a number, a boolean, an enum, a time.Time
// or a type with UnmarshalText; a value that does not parse answers 404
// before the handler runs. In is read from the query string for GET and
// DELETE and from the JSON body otherwise, then validated.
//
// opts, at most one, sets who may call the route. Route panics for a
// malformed pattern, a handler of another shape or a mistake in a v tag,
// so these show at start-up. pattern must be a constant: the generator
// reads it.
func Route(pattern string, handler any, opts ...Options) (string, http.Handler) {
	if len(opts) > 1 {
		panic(fmt.Sprintf("tether: %s: Route takes at most one Options", pattern))
	}
	var o Options
	if len(opts) == 1 {
		o = opts[0]
	}
	h, err := newHandler(pattern, handler, o)
	if err != nil {
		panic(fmt.Sprintf("tether: %s: %v", pattern, err))
	}
	return pattern, h
}

type handler struct {
	method string
	fn     reflect.Value
	params []pathParam  // taken after r, in the order of the pattern
	in     reflect.Type // nil without input
	plan   *structPlan
	upload bool
	query  bool // the input comes from the query string
	out    bool // the handler returns a value besides the error
	file   bool
	auth   *Auth // nil for a route anyone may call
	scopes []string
}

func newHandler(pattern string, fn any, o Options) (*handler, error) {
	method, path, ok := strings.Cut(pattern, " ")
	path = strings.TrimSpace(path)
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, fmt.Errorf("the pattern must start with GET, POST, PUT, PATCH or DELETE")
	}
	if !ok || !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("the path must start with /; host patterns are not supported")
	}
	if err := o.valid(); err != nil {
		return nil, err
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return nil, fmt.Errorf("the handler must be a function")
	}
	t := v.Type()
	h := &handler{method: method, fn: v, query: method == http.MethodGet || method == http.MethodDelete, params: patternParams(path)}
	if o.Auth.set() {
		auth := o.Auth
		h.auth, h.scopes = &auth, slices.Clone(o.Scopes)
	}
	if t.IsVariadic() || t.NumIn() < 1 || t.In(0) != requestType {
		return nil, fmt.Errorf("the handler must have the shape %s", shape)
	}
	switch {
	case t.NumOut() == 1 && t.Out(0) == errorType:
	case t.NumOut() == 2 && t.Out(1) == errorType:
		h.out = true
		h.file = t.Out(0) == fileType
	default:
		return nil, fmt.Errorf("the handler must have the shape %s", shape)
	}
	extra := t.NumIn() - 1 - len(h.params)
	if extra < 0 || extra > 1 {
		return nil, fmt.Errorf("the pattern has %d path parameter(s), %s; the handler takes them after r, in order, then its input: %s", len(h.params), paramList(h.params), shape)
	}
	for i := range h.params {
		p := &h.params[i]
		p.typ = t.In(1 + i)
		switch {
		case !pathType(p.typ):
			return nil, fmt.Errorf("path parameter {%s} cannot be read into %s; the handler takes the path parameters after r, in order, then its input: %s", p.name, p.typ, shape)
		case p.rest && p.typ.Kind() != reflect.String:
			return nil, fmt.Errorf("{%s...} is the rest of the path and must be a string, not %s", p.name, p.typ)
		}
	}
	if extra == 1 {
		h.in = t.In(t.NumIn() - 1)
		switch {
		case h.in == uploadType:
			if h.query {
				return nil, fmt.Errorf("an upload needs POST, PUT or PATCH")
			}
			h.upload = true
		case h.in.Kind() != reflect.Struct:
			return nil, fmt.Errorf("the input must be a struct, not %s", h.in)
		default:
			plan, err := structPlanFor(h.in)
			if err != nil {
				return nil, err
			}
			if h.query {
				for _, f := range plan.fields {
					if !queryable(f.plan) {
						return nil, fmt.Errorf("%s.%s: a %s cannot be read from the query string", h.in.Name(), f.name, f.plan.kind)
					}
				}
			}
			h.plan = plan
		}
	}
	return h, nil
}

func paramList(params []pathParam) string {
	if len(params) == 0 {
		return "none"
	}
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = "{" + p.name + "}"
	}
	return strings.Join(names, ", ")
}

// ServeHTTP runs the route: the Auth check, the path parameters, the input,
// the handler, the answer. tether writes every byte of the response, so it
// knows whether a panic can still be answered.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = withResponseHeader(r, w.Header())
	answering := false
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		if p == http.ErrAbortHandler {
			panic(p) // net/http's way to abort a response on purpose
		}
		err := fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		if answering {
			// The response has begun; all the client can still see is that
			// it breaks off.
			slog.ErrorContext(r.Context(), "tether: request failed", "method", r.Method, "path", r.URL.Path, "error", err)
			panic(http.ErrAbortHandler)
		}
		internal(w, r, err)
	}()

	if h.auth != nil {
		checked, err := h.auth.Check(r, slices.Clone(h.scopes))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if checked != nil {
			r = checked
		}
	}
	args := []reflect.Value{reflect.ValueOf(r)}
	for _, p := range h.params {
		v := reflect.New(p.typ).Elem()
		if !parsePath(r.PathValue(p.name), v) {
			// A malformed address names no resource.
			writeStatus(w, http.StatusNotFound, "notFound")
			return
		}
		args = append(args, v)
	}
	if h.in != nil {
		in, ok := h.readInput(w, r)
		if !ok {
			return
		}
		args = append(args, in)
	} else if issues := unknownQuery(r); len(issues) > 0 {
		writeIssues(w, issues)
		return
	}

	results := h.fn.Call(args)
	if errV := results[len(results)-1]; !isNilError(errV) {
		WriteError(w, r, errV.Interface().(error))
		return
	}
	answering = true
	switch {
	case !h.out:
		own(w.Header(), "")
		w.WriteHeader(http.StatusNoContent)
	case h.file:
		writeFile(w, r, results[0].Interface().(File))
	case r.Method == http.MethodPost:
		writeJSON(w, r, http.StatusCreated, results[0])
	default:
		writeJSON(w, r, http.StatusOK, results[0])
	}
}

// readInput decodes and validates the input, or answers the request itself
// and reports false.
func (h *handler) readInput(w http.ResponseWriter, r *http.Request) (reflect.Value, bool) {
	in := reflect.New(h.in).Elem()
	if h.upload {
		if issues := unknownQuery(r); len(issues) > 0 {
			writeIssues(w, issues)
			return in, false
		}
		up, err := readUpload(r)
		switch {
		case errors.Is(err, errMalformed):
			writeStatus(w, http.StatusBadRequest, "malformed")
			return in, false
		case err != nil:
			writeIssues(w, []Issue{{Field: "file", Rule: "required", Message: message("required", nil)}})
			return in, false
		}
		in.Set(reflect.ValueOf(up))
		return in, true
	}

	var issues []Issue
	if h.query {
		issues = decodeQuery(r.URL.Query(), in, h.plan)
	} else {
		if issues = unknownQuery(r); len(issues) > 0 {
			writeIssues(w, issues)
			return in, false
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeStatus(w, http.StatusRequestEntityTooLarge, "tooLarge")
			} else {
				writeStatus(w, http.StatusBadRequest, "malformed")
			}
			return in, false
		}
		issues, err = decodeBody(data, in, h.plan, h.method == http.MethodPut)
		if err != nil {
			writeStatus(w, http.StatusBadRequest, "malformed")
			return in, false
		}
	}
	if len(issues) == 0 {
		issues = validate(in, h.plan)
	}
	if len(issues) > 0 {
		writeIssues(w, issues)
		return in, false
	}
	return in, true
}

// unknownQuery rejects query parameters where the route takes none. A
// parameter the server ignores looks like a filter that worked.
func unknownQuery(r *http.Request) []Issue {
	var issues []Issue
	for k := range r.URL.Query() {
		issues = append(issues, Issue{Field: k, Rule: "unknown", Message: message("unknown", nil)})
	}
	sortIssues(issues)
	return issues
}

func sortIssues(issues []Issue) {
	sort.Slice(issues, func(i, j int) bool { return issues[i].Field < issues[j].Field })
}

// isNilError reports whether a handler's error result means success. A nil
// pointer of a project's error type inside the error interface is not nil
// to Go, but it is meant as "no error": answering it with a 500 would turn
// a classic Go mistake into an outage.
func isNilError(v reflect.Value) bool {
	if v.IsNil() {
		return true
	}
	e := v.Elem()
	return e.Kind() == reflect.Pointer && e.IsNil()
}

var errNoFile = errors.New("no file")

func readUpload(r *http.Request) (Upload, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return Upload{}, errMalformed
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return Upload{}, errNoFile
		}
		if err != nil {
			return Upload{}, errMalformed
		}
		if part.FormName() == "file" && part.FileName() != "" {
			return Upload{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Body: part}, nil
		}
	}
}
