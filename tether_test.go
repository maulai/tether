package tether

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type taskInput struct {
	Title string `json:"title" v:"required,max=10"`
	Tags  []string
}

type task struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Notes map[string]string
}

type taskFilter struct {
	Status string   `json:"status" v:"oneof=open done"`
	Limit  *int     `json:"limit" v:"max=50"`
	Tag    []string `json:"tag"`
}

func createTask(r *http.Request, in taskInput) (task, error) {
	return task{ID: "1", Title: in.Title}, nil
}

func getTask(r *http.Request, id int) (task, error) {
	if id != 1 {
		return task{}, Fail(http.StatusNotFound, "no such task")
	}
	return task{ID: "1"}, nil
}

func listTasks(r *http.Request, f taskFilter) ([]task, error) {
	return []task{{ID: f.Status}}, nil
}

func deleteTask(r *http.Request, id string) error { return nil }

func serve(t *testing.T, pattern string, h any, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(Route(pattern, h))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type %q, body %s", ct, rec.Body)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreatedWithBody(t *testing.T) {
	rec := serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":"a"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	// Nil slices and maps are written as [] and {}, never null.
	if got, want := rec.Body.String(), `{"id":"1","title":"a","tags":[],"Notes":{}}`+"\n"; got != want {
		t.Errorf("body %q, want %q", got, want)
	}
}

func TestValidationProblem(t *testing.T) {
	rec := serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":"   ","extra":1}`)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	p := decodeProblem(t, rec)
	if p.Title != "Unprocessable Entity" || p.Detail != "Please check the marked fields." {
		t.Errorf("problem %+v", p)
	}
	// Decoding problems come first; validation runs only on input that fits.
	if len(p.Errors) != 1 || p.Errors[0] != (Issue{Field: "extra", Rule: "unknown", Message: "Unknown field."}) {
		t.Errorf("errors %+v", p.Errors)
	}

	rec = serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":"   "}`)))
	p = decodeProblem(t, rec)
	if len(p.Errors) != 1 || p.Errors[0] != (Issue{Field: "title", Rule: "required", Message: "Required."}) {
		t.Errorf("errors %+v", p.Errors)
	}
}

func TestEmptyBodyIsValidated(t *testing.T) {
	rec := serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", nil))
	if p := decodeProblem(t, rec); rec.Code != 422 || p.Errors[0].Rule != "required" {
		t.Errorf("status %d, problem %+v", rec.Code, p)
	}
}

func TestMalformedAndTooLarge(t *testing.T) {
	for _, body := range []string{`{"title":`, `{"title":"a"} {}`, `[1]`} {
		rec := serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", strings.NewReader(body)))
		if body == `[1]` {
			if rec.Code != 422 {
				t.Errorf("%s: status %d", body, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, rec.Code)
		}
	}
	old := MaxBodyBytes
	MaxBodyBytes = 8
	defer func() { MaxBodyBytes = old }()
	rec := serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", strings.NewReader(`{"title":"abcdefgh"}`)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d", rec.Code)
	}
}

func TestQuery(t *testing.T) {
	rec := serve(t, "GET /tasks", listTasks, httptest.NewRequest("GET", "/tasks?status=open&tag=a&tag=b&limit=", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"open"`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	rec = serve(t, "GET /tasks", listTasks, httptest.NewRequest("GET", "/tasks?status=lost&limit=99&limit=1&page=2", nil))
	p := decodeProblem(t, rec)
	var got []string
	for _, e := range p.Errors {
		got = append(got, e.Field+":"+e.Rule)
	}
	if want := "limit:type page:unknown"; strings.Join(got, " ") != want {
		t.Errorf("errors %v, want %s", got, want)
	}
	rec = serve(t, "GET /tasks", listTasks, httptest.NewRequest("GET", "/tasks?status=lost", nil))
	if p := decodeProblem(t, rec); p.Errors[0].Rule != "oneof" {
		t.Errorf("errors %+v", p.Errors)
	}
	// An empty element keeps its place, so positions stay meaningful.
	tags := func(r *http.Request, f taskFilter) ([]string, error) { return f.Tag, nil }
	rec = serve(t, "GET /tasks", tags, httptest.NewRequest("GET", "/tasks?tag=&tag=b", nil))
	if rec.Body.String() != `["","b"]`+"\n" {
		t.Errorf("tags %s", rec.Body)
	}
}

func TestNoInputRejectsQuery(t *testing.T) {
	rec := serve(t, "GET /tasks/{id}", getTask, httptest.NewRequest("GET", "/tasks/1?debug=1", nil))
	if rec.Code != 422 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestPathAndFail(t *testing.T) {
	rec := serve(t, "GET /tasks/{id}", getTask, httptest.NewRequest("GET", "/tasks/1", nil))
	if rec.Code != 200 {
		t.Errorf("status %d", rec.Code)
	}
	rec = serve(t, "GET /tasks/{id}", getTask, httptest.NewRequest("GET", "/tasks/2", nil))
	if p := decodeProblem(t, rec); rec.Code != 404 || p.Detail != "no such task" {
		t.Errorf("status %d, problem %+v", rec.Code, p)
	}
}

func TestNoContent(t *testing.T) {
	rec := serve(t, "DELETE /tasks/{id}", deleteTask, httptest.NewRequest("DELETE", "/tasks/1", nil))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("status %d, body %q", rec.Code, rec.Body)
	}
}

// statusError has the method HTTPStatus that tether once read; it is an
// error like any other now, and MapErrors is the place to translate it.
type statusError struct{ status int }

func (e statusError) Error() string   { return "status error" }
func (e statusError) HTTPStatus() int { return e.status }

func TestHandlerErrors(t *testing.T) {
	cases := []struct {
		err    error
		status int
		detail string
		issues int
	}{
		{FieldError("title", "Taken."), 422, "Please check the marked fields.", 1},
		{errors.Join(FieldError("a", "x"), FieldError("b", "y")), 422, "Please check the marked fields.", 2},
		{Fail(http.StatusConflict, "Already issued."), 409, "Already issued.", 0},
		{Fail(http.StatusServiceUnavailable, "Back at noon."), 503, "Back at noon.", 0},
		{Fail(200, "Not an error status."), 500, "Something went wrong.", 0},
		{statusError{403}, 500, "Something went wrong.", 0},
		{errors.New("pq: connection refused"), 500, "Something went wrong.", 0},
	}
	for _, c := range cases {
		h := func(r *http.Request) error { return c.err }
		rec := serve(t, "POST /x", h, httptest.NewRequest("POST", "/x", nil))
		p := decodeProblem(t, rec)
		if rec.Code != c.status || p.Detail != c.detail || len(p.Errors) != c.issues {
			t.Errorf("%v: status %d, problem %+v", c.err, rec.Code, p)
		}
	}
}

// Middleware answers in the format of a route, keeping the headers it set.
func TestWriteError(t *testing.T) {
	gate := func(err error) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", "Session")
			WriteError(w, r, err)
		})
	}
	cases := []struct {
		err    error
		status int
		detail string
	}{
		{Fail(http.StatusUnauthorized, "Please sign in."), 401, "Please sign in."},
		{FieldError("token", "Expired."), 422, "Please check the marked fields."},
		{errors.New("session store down"), 500, "Something went wrong."},
		{nil, 500, "Something went wrong."},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		gate(c.err).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		p := decodeProblem(t, rec)
		if rec.Code != c.status || p.Detail != c.detail || rec.Header().Get("WWW-Authenticate") != "Session" {
			t.Errorf("%v: status %d, problem %+v, headers %v", c.err, rec.Code, p, rec.Header())
		}
	}
}

type projectError struct{}

func (e *projectError) Error() string { return "project error" }

func findNothing() *projectError { return nil }

func TestTypedNilError(t *testing.T) {
	h := func(r *http.Request) error {
		var err error = findNothing() // a nil *projectError, not a nil error
		return err
	}
	if rec := serve(t, "POST /x", h, httptest.NewRequest("POST", "/x", nil)); rec.Code != http.StatusNoContent {
		t.Errorf("status %d", rec.Code)
	}
}

func TestFile(t *testing.T) {
	h := func(r *http.Request) (File, error) {
		return File{Name: "Rechnung März.pdf", ContentType: "application/pdf", Body: strings.NewReader("%PDF")}, nil
	}
	rec := serve(t, "GET /doc", h, httptest.NewRequest("GET", "/doc", nil))
	if rec.Code != 200 || rec.Body.String() != "%PDF" || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline; filename*=utf-8''Rechnung%20M%C3%A4rz.pdf") {
		t.Errorf("content disposition %q", cd)
	}
}

func TestUpload(t *testing.T) {
	var got string
	h := func(r *http.Request, f Upload) error {
		b, _ := io.ReadAll(f.Body)
		got = f.Name + ":" + string(b)
		return nil
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("note", "ignored")
	fw, _ := mw.CreateFormFile("file", "a.txt")
	fw.Write([]byte("hello"))
	mw.Close()
	req := httptest.NewRequest("POST", "/files", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := serve(t, "POST /files", h, req)
	if rec.Code != 204 || got != "a.txt:hello" {
		t.Errorf("status %d, got %q", rec.Code, got)
	}

	req = httptest.NewRequest("POST", "/files", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	if rec := serve(t, "POST /files", h, req); rec.Code != 400 {
		t.Errorf("status %d", rec.Code)
	}
}

type badInput struct {
	Count int `json:"count" v:"required"`
}

func TestRoutePanics(t *testing.T) {
	cases := map[string]struct {
		pattern string
		h       any
		want    string
	}{
		"method":   {"/tasks", createTask, "must start with GET"},
		"host":     {"GET example.com/tasks", getTask, "host patterns"},
		"shape":    {"GET /tasks", func() error { return nil }, "shape"},
		"required": {"POST /x", func(r *http.Request, in badInput) error { return nil }, "use a pointer"},
		"upload":   {"GET /x", func(r *http.Request, in Upload) error { return nil }, "upload needs"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(r.(string), c.want) {
					t.Errorf("panic %v, want %q", r, c.want)
				}
			}()
			Route(c.pattern, c.h)
		})
	}
}

func TestMessageOverride(t *testing.T) {
	defer func() { msgOverride = nil }()
	Messages(map[string]string{"maxLength": "Höchstens {max} Zeichen."})
	if got := message("maxLength", map[string]string{"max": "10"}); got != "Höchstens 10 Zeichen." {
		t.Errorf("got %q", got)
	}
	if got := message("required", nil); got != "Required." {
		t.Errorf("got %q", got)
	}
}
