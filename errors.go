package tether

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

// issue is one field error as it travels in a response.
type issue struct {
	Field   string `json:"field"`
	Rule    string `json:"rule,omitempty"`
	Message string `json:"message"`
}

type fieldError struct{ issue }

func (e *fieldError) Error() string { return e.Field + ": " + e.Message }

// FieldError reports a problem with one input field, with a text written by
// the handler. The response is 422, like a failed tag rule. Combine several
// with errors.Join.
//
// field is the path in the JSON input: "username", "items.2.sku".
func FieldError(field, message string) error {
	return &fieldError{issue{Field: field, Message: message}}
}

type failError struct {
	status  int
	message string
}

func (e *failError) Error() string { return e.message }

// Fail ends the request with status and message, for example
// Fail(http.StatusConflict, "The invoice is already issued."). Use the
// net/http status constants.
func Fail(status int, message string) error {
	return &failError{status: status, message: message}
}

type problem struct {
	Type   string  `json:"type"`
	Title  string  `json:"title"`
	Status int     `json:"status"`
	Detail string  `json:"detail,omitempty"`
	Errors []issue `json:"errors,omitempty"`
}

// writeProblem writes an RFC 9457 problem document.
func writeProblem(w http.ResponseWriter, status int, detail string, issues []issue) {
	body, _ := json.Marshal(problem{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Errors: issues,
	})
	body = append(body, '\n')
	own(w.Header(), "application/problem+json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	w.Write(body)
}

func writeIssues(w http.ResponseWriter, issues []issue) {
	writeProblem(w, http.StatusUnprocessableEntity, message("invalid", nil), issues)
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
	var fail *failError
	if errors.As(err, &fail) && fail.status >= 400 && fail.status <= 599 {
		writeProblem(w, fail.status, fail.message, nil)
		return
	}
	var withStatus interface{ HTTPStatus() int }
	if errors.As(err, &withStatus) {
		if s := withStatus.HTTPStatus(); s >= 400 && s < 500 {
			writeProblem(w, s, err.Error(), nil)
			return
		} else if s >= 500 && s <= 599 {
			internal(w, r, s, err)
			return
		}
	}
	internal(w, r, http.StatusInternalServerError, err)
}

func internal(w http.ResponseWriter, r *http.Request, status int, err error) {
	slog.ErrorContext(r.Context(), "tether: request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeProblem(w, status, message("internal", nil), nil)
}

func collectIssues(err error, out []issue) []issue {
	switch e := err.(type) {
	case nil:
		return out
	case *fieldError:
		return append(out, e.issue)
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
