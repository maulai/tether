package tether

import (
	"context"
	"fmt"
	"net/http"
)

type responseKey struct{}

// ResponseHeader returns the headers of the response to r, for a handler
// that needs to set one: Cache-Control, ETag, Retry-After. The handler
// shapes stay the same; tether puts the headers into the request's context.
//
// Headers set here are sent with every answer to the request, an error as
// well: some belong exactly there (Retry-After with 429, WWW-Authenticate
// with 401). tether sets Content-Type, Content-Disposition and
// Content-Length itself and overrides them.
//
// It panics outside a handler served by Route: a header that silently goes
// nowhere is a bug.
func ResponseHeader(r *http.Request) http.Header {
	h, ok := r.Context().Value(responseKey{}).(http.Header)
	if !ok {
		panic("tether: ResponseHeader called for a request not served by tether.Route")
	}
	return h
}

// SetCookie adds a Set-Cookie header to the response, like http.SetCookie.
// Unlike http.SetCookie, an invalid cookie is an error instead of being
// dropped silently; return it, and the client gets a 500 while the cause
// goes to the log. Like every header, the cookie is sent with an error
// response too, so set a session cookie only once nothing can fail.
func SetCookie(r *http.Request, c *http.Cookie) error {
	if err := c.Valid(); err != nil {
		return fmt.Errorf("tether: cookie %q: %w", c.Name, err)
	}
	ResponseHeader(r).Add("Set-Cookie", c.String())
	return nil
}

func withResponseHeader(r *http.Request, h http.Header) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), responseKey{}, h))
}

// own sets the headers tether decides for a response, over whatever the
// handler set: a Content-Length or Content-Disposition from the handler
// would not match the body tether writes.
func own(h http.Header, contentType string) {
	h.Del("Content-Length")
	h.Del("Content-Disposition")
	if contentType == "" {
		h.Del("Content-Type")
	} else {
		h.Set("Content-Type", contentType)
	}
}
