package tether

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

var errSignedOut = Problem{Status: http.StatusUnauthorized, Code: "signedOut", Message: "Please sign in."}

type accountKey struct{}

// session reads the user from a header and allows the scope "admin" only
// to "root"; the handler finds the user in the context.
var session = Auth{Header: "X-User", Check: func(r *http.Request, scopes []string) (*http.Request, error) {
	user := r.Header.Get("X-User")
	if user == "" {
		ResponseHeader(r).Set("WWW-Authenticate", "X-User")
		return nil, errSignedOut
	}
	if slices.Contains(scopes, "admin") && user != "root" {
		return nil, Fail(http.StatusForbidden, "Only for admins.")
	}
	return r.WithContext(context.WithValue(r.Context(), accountKey{}, user)), nil
}}

func whoAmI(r *http.Request, in taskInput) (string, error) {
	return r.Context().Value(accountKey{}).(string) + ":" + in.Title, nil
}

// The Auth check runs before the input is read: a caller without
// credentials learns nothing about the input, and the handler gets what
// the check put into the context.
func TestAuth(t *testing.T) {
	signedIn := Options{Auth: session}
	admin := Options{Auth: session, Scopes: []string{"admin"}}
	req := func(user, body string) *http.Request {
		r := httptest.NewRequest("POST", "/me", strings.NewReader(body))
		if user != "" {
			r.Header.Set("X-User", user)
		}
		return r
	}
	serveWith := func(o Options, r *http.Request) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		mux.Handle(Route("POST /me", whoAmI, o))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}

	rec := serveWith(signedIn, req("", `{"title": 1}`))
	if p := decodeProblem(t, rec); rec.Code != 401 || p.Code != "signedOut" || p.Detail != "Please sign in." || rec.Header().Get("WWW-Authenticate") != "X-User" {
		t.Errorf("no user: status %d, problem %+v, headers %v", rec.Code, p, rec.Header())
	}
	if rec := serveWith(signedIn, req("ann", `{"title": "a"}`)); rec.Code != 201 || rec.Body.String() != `"ann:a"`+"\n" {
		t.Errorf("ann: status %d, body %s", rec.Code, rec.Body)
	}
	if rec := serveWith(admin, req("ann", `{"title": "a"}`)); rec.Code != 403 {
		t.Errorf("ann as admin: status %d", rec.Code)
	}
	if rec := serveWith(admin, req("root", `{"title": "a"}`)); rec.Code != 201 {
		t.Errorf("root as admin: status %d", rec.Code)
	}
	if rec := serveWith(Options{Public: true}, req("", `{}`)); rec.Code != 422 {
		t.Errorf("public: status %d", rec.Code)
	}
}

// With Auths, the credential the request carries picks the Check, in the
// order of the list; a request without one hears from the first Check, and
// a Check that fails is the answer, without a second try.
func TestAuths(t *testing.T) {
	var seen []string // the scopes each Check was handed
	who := func(prefix string, value func(*http.Request) string) Auth {
		return Auth{Check: func(r *http.Request, scopes []string) (*http.Request, error) {
			seen = append(seen, prefix+strings.Join(scopes, ","))
			v := value(r)
			switch v {
			case "":
				return nil, errSignedOut
			case "bad":
				return nil, Fail(http.StatusUnauthorized, "Not a "+prefix)
			}
			return r.WithContext(context.WithValue(r.Context(), accountKey{}, prefix+v)), nil
		}}
	}
	cookie := who("cookie ", func(r *http.Request) string {
		c, err := r.Cookie("sid")
		if err != nil {
			return ""
		}
		return c.Value
	})
	cookie.Cookie = "sid"
	bearer := who("key ", func(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") })
	bearer.Bearer = true
	reader := Options{Auths: []Auth{cookie, bearer}, Scopes: []string{"read"}}

	cases := []struct {
		name          string
		cookie, authz string
		status        int
		body          string
	}{
		{"none", "", "", 401, "Please sign in."},
		{"cookie", "ann", "", 201, `"cookie ann:a"`},
		{"key", "", "Bearer k1", 201, `"key k1:a"`},
		{"lowercase scheme", "", "bearer k1", 201, `"key bearer k1:a"`},
		{"both, first wins", "ann", "Bearer k1", 201, `"cookie ann:a"`},
		{"failed cookie, no second try", "bad", "Bearer k1", 401, "Not a cookie"},
		{"empty cookie", " ", "Bearer k1", 201, `"key k1:a"`},
		{"basic is no bearer", "", "Basic YTpi", 401, "Please sign in."},
		{"bearer without token", "", "Bearer ", 401, "Please sign in."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen = nil
			r := httptest.NewRequest("POST", "/me", strings.NewReader(`{"title": "a"}`))
			if c.cookie != "" {
				r.Header.Set("Cookie", "sid="+strings.TrimSpace(c.cookie))
			}
			if c.authz != "" {
				r.Header.Set("Authorization", c.authz)
			}
			mux := http.NewServeMux()
			mux.Handle(Route("POST /me", whoAmI, reader))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, r)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) {
				t.Errorf("status %d, body %s; want %d with %s", rec.Code, rec.Body, c.status, c.body)
			}
			if len(seen) != 1 || !strings.HasSuffix(seen[0], " read") {
				t.Errorf("checks called: %q, want one with the scopes", seen)
			}
		})
	}
}

func TestOptionsMistakes(t *testing.T) {
	check := func(r *http.Request, _ []string) (*http.Request, error) { return r, nil }
	cases := map[string]struct {
		opts []Options
		want string
	}{
		"two":            {[]Options{{}, {}}, "at most one Options"},
		"public auth":    {[]Options{{Public: true, Auth: Auth{Bearer: true, Check: check}}}, "either Public or has an Auth"},
		"scopes":         {[]Options{{Scopes: []string{"admin"}}}, "Scopes need an Auth"},
		"two carriers":   {[]Options{{Auth: Auth{Bearer: true, Header: "X-Key", Check: check}}}, "exactly one of Cookie, Header and Bearer"},
		"no carrier":     {[]Options{{Auth: Auth{Check: check}}}, "exactly one of Cookie, Header and Bearer"},
		"without check":  {[]Options{{Auth: Auth{Cookie: "session"}}}, "needs a Check"},
		"auth and auths": {[]Options{{Auth: Auth{Bearer: true, Check: check}, Auths: []Auth{{Cookie: "a", Check: check}, {Cookie: "b", Check: check}}}}, "an Auth or Auths, not both"},
		"one of auths":   {[]Options{{Auths: []Auth{{Cookie: "a", Check: check}}}}, "two or more; for one way in, use Auth"},
		"public auths":   {[]Options{{Public: true, Auths: []Auth{{Cookie: "a", Check: check}, {Cookie: "b", Check: check}}}}, "either Public or has an Auth"},
		"same cookie":    {[]Options{{Auths: []Auth{{Cookie: "a", Check: check}, {Cookie: "a", Check: check}}}}, "two Auths read the cookie a"},
		"same header":    {[]Options{{Auths: []Auth{{Header: "x-key", Check: check}, {Header: "X-Key", Check: check}}}}, "two Auths read the header X-Key"},
		"bearer header":  {[]Options{{Auths: []Auth{{Bearer: true, Check: check}, {Header: "authorization", Check: check}}}}, "two Auths read the header Authorization"},
		"bad of auths":   {[]Options{{Auths: []Auth{{Cookie: "a", Check: check}, {Cookie: "b"}}}}, "needs a Check"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), c.want) {
					t.Errorf("panic %v, want %q", r, c.want)
				}
			}()
			Route("POST /me", whoAmI, c.opts...)
		})
	}
}

// A Problem carries its code to the client; With changes the text and
// keeps the case, also for errors.Is.
func TestProblem(t *testing.T) {
	suspended := Problem{Status: http.StatusForbidden, Code: "tenantSuspended", Message: "This archive is suspended."}
	err := fmt.Errorf("load: %w", suspended.With("Suspended: unpaid."))
	if !errors.Is(err, suspended) || errors.Is(err, errSignedOut) || errors.Is(Fail(403, "x"), suspended) {
		t.Error("errors.Is does not follow the code")
	}
	h := func(r *http.Request) error { return err }
	rec := serve(t, "POST /x", h, httptest.NewRequest("POST", "/x", nil))
	if p := decodeProblem(t, rec); rec.Code != 403 || p.Code != "tenantSuspended" || p.Detail != "Suspended: unpaid." {
		t.Errorf("status %d, problem %+v", rec.Code, p)
	}
	if rec := serve(t, "POST /x", func(r *http.Request) error { return Fail(409, "Taken.") }, httptest.NewRequest("POST", "/x", nil)); strings.Contains(rec.Body.String(), `"code"`) {
		t.Errorf("Fail has a code: %s", rec.Body)
	}
}

// capture records what tether logs during f.
func capture(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	f()
	return buf.String()
}

// A 500 names its instance in the response and in the log line with the
// cause, which the response does not show.
func TestInstance(t *testing.T) {
	var rec *httptest.ResponseRecorder
	log := capture(t, func() {
		rec = serve(t, "POST /x", func(r *http.Request) error { return errors.New("pq: connection refused") }, httptest.NewRequest("POST", "/x", nil))
	})
	p := decodeProblem(t, rec)
	if rec.Code != 500 || !strings.HasPrefix(p.Instance, "urn:uuid:") || len(p.Instance) != len("urn:uuid:")+36 || strings.Contains(rec.Body.String(), "pq:") {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(log, "instance="+p.Instance) || !strings.Contains(log, "pq: connection refused") {
		t.Errorf("log %q", log)
	}
	if again := decodeProblem(t, serve(t, "POST /x", func(r *http.Request) error { return errors.New("x") }, httptest.NewRequest("POST", "/x", nil))); again.Instance == p.Instance {
		t.Error("two failures share an instance")
	}
	if p := decodeProblem(t, serve(t, "POST /x", func(r *http.Request) error { return Fail(409, "x") }, httptest.NewRequest("POST", "/x", nil))); p.Instance != "" {
		t.Errorf("a 409 has an instance: %+v", p)
	}
}

// A panic in a handler or in a check is answered like any other unexpected
// error, instead of a connection that breaks off; the client reads it as
// a 500, not as an unreachable server.
func TestPanicIsAnswered(t *testing.T) {
	boom := func(r *http.Request) error { panic("boom") }
	var rec *httptest.ResponseRecorder
	log := capture(t, func() { rec = serve(t, "POST /x", boom, httptest.NewRequest("POST", "/x", nil)) })
	p := decodeProblem(t, rec)
	if rec.Code != 500 || p.Instance == "" || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(log, "panic: boom") || !strings.Contains(log, "auth_test.go") {
		t.Errorf("log without the panic and its stack: %q", log)
	}

	panicky := Auth{Bearer: true, Check: func(r *http.Request, _ []string) (*http.Request, error) { panic("check") }}
	mux := http.NewServeMux()
	mux.Handle(Route("POST /me", whoAmI, Options{Auth: panicky}))
	rec = httptest.NewRecorder()
	capture(t, func() { mux.ServeHTTP(rec, httptest.NewRequest("POST", "/me", nil)) })
	if rec.Code != 500 {
		t.Errorf("panic in a check: status %d", rec.Code)
	}
}

// http.ErrAbortHandler aborts on purpose and passes on, and a panic once
// the response has begun can only break it off.
func TestPanicAborts(t *testing.T) {
	cases := map[string]any{
		"abort": func(r *http.Request) error { panic(http.ErrAbortHandler) },
		"while writing": func(r *http.Request) (File, error) {
			return File{Body: panicReader{}}, nil
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != http.ErrAbortHandler {
					t.Errorf("panic %v", r)
				}
			}()
			capture(t, func() { serve(t, "GET /x", h, httptest.NewRequest("GET", "/x", nil)) })
		})
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("disk gone") }
