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
package tether

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

// MaxBodyBytes limits a JSON request body. Uploads do not count against it;
// they are streamed to the handler.
var MaxBodyBytes int64 = 1 << 20

var requestType = reflect.TypeFor[*http.Request]()

// Route turns handler into an http.Handler for pattern, a net/http
// ServeMux pattern with a method such as "PUT /v1/tasks/{id}". The two
// results fit mux.Handle directly.
//
// handler is a named function with one of these shapes:
//
//	func(r *http.Request) (Out, error)
//	func(r *http.Request, in In) (Out, error)
//	func(r *http.Request) error
//	func(r *http.Request, in In) error
//
// In is read from the query string for GET and DELETE and from the JSON
// body otherwise, then validated. Route panics for a malformed pattern, a
// handler of another shape or a mistake in a v tag, so these show at
// start-up. pattern must be a constant: the generator reads it.
func Route(pattern string, handler any) (string, http.Handler) {
	h, err := newHandler(pattern, handler)
	if err != nil {
		panic(fmt.Sprintf("tether: %s: %v", pattern, err))
	}
	return pattern, h
}

type handler struct {
	method string
	fn     reflect.Value
	in     reflect.Type // nil without input
	plan   *structPlan
	upload bool
	query  bool // the input comes from the query string
	out    bool // the handler returns a value besides the error
	file   bool
}

func newHandler(pattern string, fn any) (*handler, error) {
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
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return nil, fmt.Errorf("the handler must be a function")
	}
	t := v.Type()
	h := &handler{method: method, fn: v, query: method == http.MethodGet || method == http.MethodDelete}
	shape := "func(*http.Request[, In]) ([Out, ]error)"
	if t.IsVariadic() || t.NumIn() < 1 || t.NumIn() > 2 || t.In(0) != requestType {
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
	if t.NumIn() == 2 {
		h.in = t.In(1)
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

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = withResponseHeader(r, w.Header())
	args := []reflect.Value{reflect.ValueOf(r)}
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
		writeError(w, r, errV.Interface().(error))
		return
	}
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
			writeProblem(w, http.StatusBadRequest, message("malformed", nil), nil)
			return in, false
		case err != nil:
			writeIssues(w, []issue{{Field: "file", Rule: "required", Message: message("required", nil)}})
			return in, false
		}
		in.Set(reflect.ValueOf(up))
		return in, true
	}

	var issues []issue
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
				writeProblem(w, http.StatusRequestEntityTooLarge, message("tooLarge", nil), nil)
			} else {
				writeProblem(w, http.StatusBadRequest, message("malformed", nil), nil)
			}
			return in, false
		}
		issues, err = decodeBody(data, in, h.plan)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, message("malformed", nil), nil)
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
func unknownQuery(r *http.Request) []issue {
	var issues []issue
	for k := range r.URL.Query() {
		issues = append(issues, issue{Field: k, Rule: "unknown", Message: message("unknown", nil)})
	}
	sortIssues(issues)
	return issues
}

func sortIssues(issues []issue) {
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
