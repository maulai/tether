package tether

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

// Issue is one field error: the path of the field in the JSON input, the
// rule that failed (empty for a FieldError) and the finished text. A 422
// response lists them in errors.
type Issue struct {
	Field   string `json:"field"`
	Rule    string `json:"rule,omitempty"`
	Message string `json:"message"`
}

type fieldError struct{ Issue }

func (e *fieldError) Error() string { return e.Field + ": " + e.Message }

// FieldError reports a problem with one input field, with a text written by
// the handler. The response is 422, like a failed tag rule. Combine several
// with errors.Join.
//
// field is the path in the JSON input: "username", "items.2.sku".
func FieldError(field, message string) error {
	return &fieldError{Issue{Field: field, Message: message}}
}

// Problem is an error the client sees with its status and text. A Problem
// with a Code is a case the client tells apart from others with the same
// status, to react to it: declare it once as a package variable, and the
// generator gives the TypeScript client a function that recognises it.
//
//	// ErrMFARequired: the session waits for the second factor.
//	var ErrMFARequired = tether.Problem{Status: http.StatusForbidden, Code: "mfaRequired", Message: "Two-factor sign-in required."}
//
//	return ErrMFARequired
//
// Code is lowerCamel, like the codes of tether's own messages. A status
// outside 400 to 599 is a mistake and answers 500.
type Problem struct {
	Status  int
	Code    string
	Message string
}

func (p Problem) Error() string {
	if p.Code == "" {
		return p.Message
	}
	return p.Code + ": " + p.Message
}

// With returns the problem with another text, for a message that names
// what the declaration cannot know. Code and status stay, and errors.Is
// still recognises it.
func (p Problem) With(message string) Problem {
	p.Message = message
	return p
}

// Is reports whether target is a Problem with the same code, so a problem
// given another text by With is still the same case.
func (p Problem) Is(target error) bool {
	t, ok := target.(Problem)
	return ok && p.Code != "" && t.Code == p.Code
}

// Fail ends the request with status and message, for example
// Fail(http.StatusConflict, "The invoice is already issued."). It is a
// Problem without a code, for a text the client shows rather than reacts
// to. Use the net/http status constants.
func Fail(status int, message string) error {
	return Problem{Status: status, Message: message}
}

type problem struct {
	Type     string  `json:"type"`
	Title    string  `json:"title"`
	Status   int     `json:"status"`
	Code     string  `json:"code,omitempty"`
	Detail   string  `json:"detail,omitempty"`
	Instance string  `json:"instance,omitempty"`
	Errors   []Issue `json:"errors,omitempty"`
}

// writeProblem writes an RFC 9457 problem document.
func writeProblem(w http.ResponseWriter, p problem) {
	p.Type, p.Title = "about:blank", http.StatusText(p.Status)
	body, _ := json.Marshal(p)
	body = append(body, '\n')
	own(w.Header(), "application/problem+json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(p.Status)
	w.Write(body)
}

func writeStatus(w http.ResponseWriter, status int, code string) {
	writeProblem(w, problem{Status: status, Detail: message(code, nil)})
}

func writeIssues(w http.ResponseWriter, issues []Issue) {
	writeProblem(w, problem{Status: http.StatusUnprocessableEntity, Detail: message("invalid", nil), Errors: issues})
}

// WriteError answers err the way a route answers a handler's error: the
// same problem document, the same status table, after MapErrors. It is for
// code outside a route that answers a request itself, such as a CSRF check
// or a rate limit in middleware, so the client sees one error format. Call
// it before anything is written to w.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		// answering a nil error as success could open a gate that meant
		// to refuse; as a 500 the mistake shows in the log.
		internal(w, r, errors.New("tether: WriteError called with a nil error"))
		return
	}
	writeError(w, r, mapError(err))
}

// writeError answers a handler's error. Field errors anywhere in the error
// tree make it a 422 with all of them. Anything tether cannot classify is
// a 500 whose cause goes to the log, never to the client: technical details
// do not belong in a response.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if issues := collectIssues(err, nil); len(issues) > 0 {
		writeIssues(w, issues)
		return
	}
	var p Problem
	var ptr *Problem
	if errors.As(err, &ptr) && ptr != nil {
		p = *ptr
	} else {
		errors.As(err, &p)
	}
	if p.Status >= 400 && p.Status <= 599 {
		writeProblem(w, problem{Status: p.Status, Code: p.Code, Detail: p.Message})
		return
	}
	internal(w, r, err)
}

// internal answers 500 for a cause the client must not see. The response
// and the log line carry the same instance, so a person who reports the
// error page names the log entry with the cause.
func internal(w http.ResponseWriter, r *http.Request, err error) {
	instance := newInstance()
	slog.ErrorContext(r.Context(), "tether: request failed", "method", r.Method, "path", r.URL.Path, "instance", instance, "error", err)
	writeProblem(w, problem{Status: http.StatusInternalServerError, Detail: message("internal", nil), Instance: instance})
}

// newInstance returns a random UUID as a URN, an absolute URI as RFC 9457
// asks for the instance.
func newInstance() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Issues lists the field errors in err as a 422 response would: those of
// Validate, of FieldError, and of errors.Join over them. It is nil when err
// holds none. Tests use it to check which fields fail and why.
func Issues(err error) []Issue {
	return collectIssues(err, nil)
}

func collectIssues(err error, out []Issue) []Issue {
	switch e := err.(type) {
	case nil:
		return out
	case *fieldError:
		return append(out, e.Issue)
	case interface{ Unwrap() []error }:
		for _, sub := range e.Unwrap() {
			out = collectIssues(sub, out)
		}
		return out
	case interface{ Unwrap() error }:
		return collectIssues(e.Unwrap(), out)
	}
	return out
}
