package tether

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hexID stands for a type like uuid.UUID: it reads itself with UnmarshalText.
type hexID [2]byte

func (h *hexID) UnmarshalText(b []byte) error {
	_, err := fmt.Sscanf(string(b), "%02x%02x", &h[0], &h[1])
	if err != nil || len(b) != 4 {
		return errors.New("not a hex id")
	}
	return nil
}

func (h hexID) MarshalText() ([]byte, error) { return []byte(fmt.Sprintf("%02x%02x", h[0], h[1])), nil }

// code is a text type with a Format, registered once for the whole test.
type code [3]byte

func (c *code) UnmarshalText(b []byte) error {
	if len(b) != 3 {
		return errors.New("not a code")
	}
	copy(c[:], b)
	return nil
}

func (c code) MarshalText() ([]byte, error) { return c[:], nil }

// The conformance vectors register its Format, code3: two letters and a
// digit, or two capitals, which UnmarshalText then refuses.

// Path parameters are the handler's arguments after r, in the order of the
// pattern, parsed into their types.
func TestPathParameters(t *testing.T) {
	h := func(r *http.Request, id hexID, n int, c color) (map[string]string, error) {
		return map[string]string{"id": fmt.Sprint(id[0], id[1]), "n": fmt.Sprint(n), "color": string(c)}, nil
	}
	rec := serve(t, "GET /x/{id}/{n}/{color}", h, httptest.NewRequest("GET", "/x/0a0b/7/red", nil))
	if rec.Code != 200 || rec.Body.String() != `{"color":"red","id":"10 11","n":"7"}`+"\n" {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/x/zz/7/red", "/x/0a0b/seven/red", "/x/0a0b/7/purple"} {
		rec := serve(t, "GET /x/{id}/{n}/{color}", h, httptest.NewRequest("GET", path, nil))
		if p := decodeProblem(t, rec); rec.Code != 404 || p.Detail != "Not found." {
			t.Errorf("%s: status %d, problem %+v", path, rec.Code, p)
		}
	}
}

// A path parameter and the input come together: the parameters first, the
// input last.
func TestPathParameterWithInput(t *testing.T) {
	h := func(r *http.Request, id int, in taskInput) (task, error) {
		return task{ID: fmt.Sprint(id), Title: in.Title}, nil
	}
	rec := serve(t, "PUT /tasks/{id}", h, httptest.NewRequest("PUT", "/tasks/3", strings.NewReader(`{"title":"a","Tags":[]}`)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"3"`) {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
}

// The rest of a path is a string, and may be empty.
func TestRestParameter(t *testing.T) {
	h := func(r *http.Request, path string) (string, error) { return path, nil }
	for path, want := range map[string]string{"/files/a/b.txt": `"a/b.txt"`, "/files/": `""`} {
		rec := serve(t, "GET /files/{path...}", h, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
			t.Errorf("%s: status %d, body %s", path, rec.Code, rec.Body)
		}
	}
}

// A path that does not match the Format of its type names no resource.
func TestPathParameterFormat(t *testing.T) {
	h := func(r *http.Request, c code) (string, error) { return string(c[:]), nil }
	for path, status := range map[string]int{"/c/ab1": 200, "/c/abc": 404, "/c/AB": 404} {
		if rec := serve(t, "GET /c/{c}", h, httptest.NewRequest("GET", path, nil)); rec.Code != status {
			t.Errorf("%s: status %d, want %d", path, rec.Code, status)
		}
	}
}

// A handler that does not take the pattern's parameters as written is a
// mistake that stops the program at start-up.
func TestPathParameterMistakes(t *testing.T) {
	cases := map[string]struct {
		pattern string
		h       any
		want    string
	}{
		"missing":      {"GET /x/{id}", func(r *http.Request) error { return nil }, "the pattern has 1 path parameter(s), {id}"},
		"input first":  {"PUT /x/{id}", func(r *http.Request, in taskInput) error { return nil }, "path parameter {id} cannot be read into tether.taskInput"},
		"too many":     {"GET /x", func(r *http.Request, id int, n int) error { return nil }, "the pattern has 0 path parameter(s), none"},
		"rest":         {"GET /x/{p...}", func(r *http.Request, p int) error { return nil }, "{p...} is the rest of the path and must be a string"},
		"unparseable":  {"GET /x/{id}", func(r *http.Request, id []string) error { return nil }, "cannot be read into []string"},
		"input of GET": {"GET /x/{id}", func(r *http.Request, id int, f Upload) error { return nil }, "upload needs"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), c.want) {
					t.Errorf("panic %v, want %q", r, c.want)
				}
			}()
			Route(c.pattern, c.h)
		})
	}
}

type formatInput struct {
	Code  code   `json:"code" v:"required"`
	Maybe *code  `json:"maybe"`
	Codes []code `json:"codes"`
}

// A type with a Format is checked against it before UnmarshalText, so the
// client gets the Format's message; "" means "not given".
func TestFormatInBody(t *testing.T) {
	h := func(r *http.Request, in formatInput) (formatInput, error) { return in, nil }
	cases := []struct{ body, want string }{
		{`{"code": "ab1", "maybe": "", "codes": ["zz9"]}`, ""},
		{`{"code": "abc"}`, "code:code3"},
		{`{"code": "AB"}`, "code:type"},
		{`{"code": ""}`, "code:required"},
		{`{"code": "ab1", "maybe": "x", "codes": ["ab1", "x"]}`, "codes.1:code3 maybe:code3"},
		{`{"code": 7}`, "code:type"},
	}
	for _, c := range cases {
		rec := serve(t, "POST /f", h, httptest.NewRequest("POST", "/f", strings.NewReader(c.body)))
		if c.want == "" {
			if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"maybe":null`) {
				t.Errorf("%s: status %d, body %s", c.body, rec.Code, rec.Body)
			}
			continue
		}
		if got := fieldRules(t, rec); got != c.want {
			t.Errorf("%s: %s, want %s", c.body, got, c.want)
		}
	}
	rec := serve(t, "POST /f", h, httptest.NewRequest("POST", "/f", strings.NewReader(`{"code": "abc"}`)))
	if p := decodeProblem(t, rec); p.Errors[0].Message != "Use two letters and a digit." {
		t.Errorf("message %q", p.Errors[0].Message)
	}
}

type formatFilter struct {
	Code  *code  `json:"code"`
	Codes []code `json:"codes"`
}

func TestFormatInQuery(t *testing.T) {
	h := func(r *http.Request, f formatFilter) error { return nil }
	for query, want := range map[string]string{"?code=ab1&codes=zz9": "", "?code=": "", "?code=abc": "code:code3", "?codes=ab1&codes=AB": "codes.1:type"} {
		rec := serve(t, "GET /f", h, httptest.NewRequest("GET", "/f"+query, nil))
		if want == "" {
			if rec.Code != 204 {
				t.Errorf("%s: status %d, body %s", query, rec.Code, rec.Body)
			}
			continue
		}
		if got := fieldRules(t, rec); got != want {
			t.Errorf("%s: %s, want %s", query, got, want)
		}
	}
}

// The name of a Format is a rule like a Pattern, for plain strings too.
func TestFormatAsRule(t *testing.T) {
	type in struct {
		Text string `json:"text" v:"code3"`
	}
	if got := Issues(Validate(in{Text: "abc"})); len(got) != 1 || got[0].Rule != "code3" {
		t.Errorf("issues %+v", got)
	}
}

func TestFormatMistakes(t *testing.T) {
	cases := map[string]func(){
		"no UnmarshalText": func() { Format[color]("colorFormat", `[a-z]+`, "x") },
		"time":             func() { Format[time.Time]("timeFormat", `\d+`, "x") },
		"twice":            func() { Format[code]("code3b", `x`, "x") },
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "tether: format") {
					t.Errorf("panic %v", r)
				}
			}()
			f()
		})
	}
}

var errNoRows = errors.New("no rows in result set")

func TestMapErrors(t *testing.T) {
	defer func() { mapper = nil }()
	MapErrors(func(err error) error {
		switch {
		case errors.Is(err, errNoRows):
			return Fail(http.StatusNotFound, "No such task.")
		case err.Error() == "dup":
			return FieldError("title", "Taken.")
		}
		return nil // nil keeps the original error
	})
	for _, c := range []struct {
		err    error
		status int
	}{
		{fmt.Errorf("load task: %w", errNoRows), 404},
		{errors.New("dup"), 422},
		{errors.New("disk full"), 500},
	} {
		h := func(r *http.Request) error { return c.err }
		if rec := serve(t, "POST /x", h, httptest.NewRequest("POST", "/x", nil)); rec.Code != c.status {
			t.Errorf("%v: status %d, want %d", c.err, rec.Code, c.status)
		}
	}
	// Middleware answering with WriteError gets the same mapping.
	direct := httptest.NewRecorder()
	WriteError(direct, httptest.NewRequest("GET", "/", nil), errNoRows)
	if direct.Code != 404 {
		t.Errorf("WriteError: status %d", direct.Code)
	}
	// tether's own validation errors do not pass through the mapping.
	rec := serve(t, "POST /tasks", createTask, httptest.NewRequest("POST", "/tasks", strings.NewReader(`{}`)))
	if rec.Code != 422 {
		t.Errorf("validation: status %d", rec.Code)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("a second MapErrors did not panic")
		}
	}()
	MapErrors(func(err error) error { return err })
}
