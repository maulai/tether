package tether

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hexID stands for a type like uuid.UUID: it parses itself with UnmarshalText.
type hexID [2]byte

func (h *hexID) UnmarshalText(b []byte) error {
	_, err := fmt.Sscanf(string(b), "%02x%02x", &h[0], &h[1])
	if err != nil || len(b) != 4 {
		return errors.New("not a hex id")
	}
	return nil
}

func TestPathValue(t *testing.T) {
	h := func(r *http.Request) (map[string]string, error) {
		id, err := PathValue[hexID](r, "id")
		if err != nil {
			return nil, err
		}
		n, err := PathValue[int](r, "n")
		if err != nil {
			return nil, err
		}
		c, err := PathValue[color](r, "color")
		if err != nil {
			return nil, err
		}
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

func TestPathValueMistakes(t *testing.T) {
	cases := map[string]func(r *http.Request) error{
		"unknown parameter": func(r *http.Request) error { _, err := PathValue[string](r, "nope"); return err },
		"unsupported type":  func(r *http.Request) error { _, err := PathValue[[]string](r, "id"); return err },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "tether: PathValue") {
					t.Errorf("panic %v", r)
				}
			}()
			// Called directly, with the pattern the mux would have set.
			req := httptest.NewRequest("GET", "/x/1", nil)
			req.Pattern = "GET /x/{id}"
			req.SetPathValue("id", "1")
			h(req)
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
