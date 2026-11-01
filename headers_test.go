package tether

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var modTime = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// fileHandler serves sixteen bytes; etag, when set, goes out as the ETag.
func fileHandler(modTime time.Time, etag string, seekable bool) func(r *http.Request) (File, error) {
	return func(r *http.Request) (File, error) {
		if etag != "" {
			ResponseHeader(r).Set("ETag", etag)
		}
		var body io.Reader = strings.NewReader("0123456789abcdef")
		if !seekable {
			body = io.MultiReader(body) // hides Seek
		}
		return File{Name: "a.txt", ContentType: "text/plain", Body: body, ModTime: modTime}, nil
	}
}

func fileRequest(method string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, "/f", nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return req
}

func TestRange(t *testing.T) {
	h := fileHandler(modTime, "", true)
	rec := serve(t, "GET /f", h, fileRequest("GET", "Range", "bytes=0-9"))
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "0123456789" || rec.Header().Get("Content-Range") != "bytes 0-9/16" {
		t.Errorf("status %d, body %q, headers %v", rec.Code, rec.Body, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "inline; filename=a.txt" {
		t.Errorf("a partial answer keeps its disposition, got %q", cd)
	}
	if rec := serve(t, "GET /f", h, fileRequest("GET", "Range", "bytes=100-200")); rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("invalid range: status %d", rec.Code)
	}
}

func TestHeadOnFile(t *testing.T) {
	// ServeMux sends HEAD to the GET pattern; no pattern of its own is needed.
	rec := serve(t, "GET /f", fileHandler(modTime, "", true), fileRequest("HEAD"))
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "16" || rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("status %d, body %q, headers %v", rec.Code, rec.Body, rec.Header())
	}
}

func TestConditional(t *testing.T) {
	since := modTime.Format(http.TimeFormat)
	if rec := serve(t, "GET /f", fileHandler(modTime, "", true), fileRequest("GET", "If-Modified-Since", since)); rec.Code != http.StatusNotModified {
		t.Errorf("If-Modified-Since: status %d", rec.Code)
	}
	if rec := serve(t, "GET /f", fileHandler(time.Time{}, "", true), fileRequest("GET", "If-Modified-Since", since)); rec.Code != 200 {
		t.Errorf("without ModTime: status %d", rec.Code)
	}
	if rec := serve(t, "GET /f", fileHandler(time.Time{}, `"v1"`, true), fileRequest("GET", "If-None-Match", `"v1"`)); rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: status %d", rec.Code)
	}
}

func TestFileWithoutRange(t *testing.T) {
	rec := serve(t, "GET /f", fileHandler(modTime, "", false), fileRequest("GET", "Range", "bytes=0-9"))
	if rec.Code != 200 || rec.Body.String() != "0123456789abcdef" {
		t.Errorf("not seekable: status %d, body %q", rec.Code, rec.Body)
	}
	post := func(r *http.Request) (File, error) {
		return File{ContentType: "text/plain", Body: strings.NewReader("0123456789abcdef")}, nil
	}
	rec = serve(t, "POST /f", post, fileRequest("POST", "Range", "bytes=0-9"))
	if rec.Code != 200 || rec.Body.String() != "0123456789abcdef" || rec.Header().Get("Accept-Ranges") != "" {
		t.Errorf("POST: status %d, body %q, headers %v", rec.Code, rec.Body, rec.Header())
	}
}

func TestAttachment(t *testing.T) {
	cases := []struct {
		file File
		want string
	}{
		{File{Name: "report.csv", Attachment: true}, "attachment; filename=report.csv"},
		{File{Name: "Bericht März.csv", Attachment: true}, "attachment; filename*=utf-8''Bericht%20M%C3%A4rz.csv"},
		{File{Attachment: true}, "attachment"},
		{File{Name: "report.csv"}, "inline; filename=report.csv"},
		{File{}, ""},
	}
	for _, c := range cases {
		h := func(r *http.Request) (File, error) {
			ResponseHeader(r).Set("Content-Disposition", "inline; filename=wrong.txt")
			f := c.file
			f.Body = strings.NewReader("x")
			return f, nil
		}
		rec := serve(t, "GET /f", h, fileRequest("GET"))
		if got := rec.Header().Get("Content-Disposition"); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.file, got, c.want)
		}
	}
}

func TestResponseHeaders(t *testing.T) {
	ok := func(r *http.Request) (task, error) {
		ResponseHeader(r).Set("Cache-Control", "no-store")
		ResponseHeader(r).Set("Content-Type", "text/html") // tether's own header wins
		ResponseHeader(r).Set("Content-Length", "999")
		if err := SetCookie(r, &http.Cookie{Name: "session", Value: "abc", HttpOnly: true}); err != nil {
			return task{}, err
		}
		return task{ID: "1"}, nil
	}
	rec := serve(t, "GET /x", ok, httptest.NewRequest("GET", "/x", nil))
	h := rec.Header()
	if rec.Code != 200 || h.Get("Cache-Control") != "no-store" || h.Get("Set-Cookie") != "session=abc; HttpOnly" {
		t.Errorf("status %d, headers %v", rec.Code, h)
	}
	if h.Get("Content-Type") != "application/json" || h.Get("Content-Length") != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("tether's headers: %v, body %q", h, rec.Body)
	}

	// Headers count on an error response too: Retry-After belongs to a 429.
	limited := func(r *http.Request) error {
		ResponseHeader(r).Set("Retry-After", "30")
		SetCookie(r, &http.Cookie{Name: "session", Value: "", MaxAge: -1})
		return Fail(http.StatusTooManyRequests, "Slow down.")
	}
	rec = serve(t, "POST /x", limited, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "30" || !strings.HasPrefix(rec.Header().Get("Set-Cookie"), "session=; Max-Age=0") {
		t.Errorf("status %d, headers %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("content type %q", rec.Header().Get("Content-Type"))
	}
}

func TestInvalidCookie(t *testing.T) {
	var cookieErr error
	h := func(r *http.Request) error {
		cookieErr = SetCookie(r, &http.Cookie{Name: "bad name", Value: "x"})
		return cookieErr
	}
	rec := serve(t, "POST /x", h, httptest.NewRequest("POST", "/x", nil))
	if cookieErr == nil || !strings.Contains(cookieErr.Error(), `cookie "bad name"`) {
		t.Errorf("error %v", cookieErr)
	}
	if rec.Code != 500 || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("status %d, headers %v", rec.Code, rec.Header())
	}
}

func TestResponseHeaderOutsideRoute(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "not served by tether.Route") {
			t.Errorf("panic %v", r)
		}
	}()
	ResponseHeader(httptest.NewRequest("GET", "/", nil))
}

func TestNoContentDropsBodyHeaders(t *testing.T) {
	h := func(r *http.Request) error {
		ResponseHeader(r).Set("Content-Type", "text/plain")
		ResponseHeader(r).Set("X-Kept", "1")
		return nil
	}
	rec := serve(t, "DELETE /x", h, httptest.NewRequest("DELETE", "/x", nil))
	if rec.Code != 204 || rec.Header().Get("Content-Type") != "" || rec.Header().Get("X-Kept") != "1" {
		t.Errorf("status %d, headers %v", rec.Code, rec.Header())
	}
}
