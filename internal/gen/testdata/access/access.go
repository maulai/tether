// Package access is full of mistakes in who may call a route; the expected
// report is want/errors.txt.
package access

import (
	"net/http"

	"github.com/maulai/tether"
)

func check(r *http.Request, scopes []string) (*http.Request, error) { return r, nil }

// Session is fine.
var Session = tether.Auth{Cookie: "session", Check: check}

// Both names two places for its credential.
var Both = tether.Auth{Cookie: "session", Bearer: true, Check: check}

func options() tether.Options { return tether.Options{Auth: Session} }

func List(r *http.Request) error   { return nil }
func Open(r *http.Request) error   { return nil }
func Mixed(r *http.Request) error  { return nil }
func Scoped(r *http.Request) error { return nil }
func Inline(r *http.Request) error { return nil }
func Called(r *http.Request) error { return nil }
func Double(r *http.Request) error { return nil }
func Either(r *http.Request) error { return nil }
func Lone(r *http.Request) error   { return nil }
func Twice(r *http.Request) error  { return nil }
func Clash(r *http.Request) error  { return nil }
func Header(r *http.Request) error { return nil }
func Plus(r *http.Request) error   { return nil }
func Loose(r *http.Request) error  { return nil }
func Free(r *http.Request) error   { return nil }

// Key is fine.
var Key = tether.Auth{Bearer: true, Check: check}

// Other reads the cookie Session reads.
var Other = tether.Auth{Cookie: "session", Check: check}

// Raw reads the header a bearer token travels in.
var Raw = tether.Auth{Header: "authorization", Check: check}

var both = []tether.Auth{Session, Key}

func Routes(mux *http.ServeMux) {
	mux.Handle(tether.Route("GET /list", List, tether.Options{Auth: Session}))
	mux.Handle(tether.Route("GET /open", Open))
	mux.Handle(tether.Route("GET /mixed", Mixed, tether.Options{Auth: Session, Public: true}))
	mux.Handle(tether.Route("GET /scoped", Scoped, tether.Options{Scopes: []string{"admin"}}))
	mux.Handle(tether.Route("GET /inline", Inline, tether.Options{Auth: tether.Auth{Bearer: true, Check: check}}))
	mux.Handle(tether.Route("GET /called", Called, options()))
	mux.Handle(tether.Route("GET /double", Double, tether.Options{Auth: Both}))
	mux.Handle(tether.Route("GET /either", Either, tether.Options{Auths: []tether.Auth{Session, Key}}))
	mux.Handle(tether.Route("GET /lone", Lone, tether.Options{Auths: []tether.Auth{Session}}))
	mux.Handle(tether.Route("GET /twice", Twice, tether.Options{Auths: []tether.Auth{Key, Key}}))
	mux.Handle(tether.Route("GET /clash", Clash, tether.Options{Auths: []tether.Auth{Session, Other}}))
	mux.Handle(tether.Route("GET /header", Header, tether.Options{Auths: []tether.Auth{Key, Raw}}))
	mux.Handle(tether.Route("GET /plus", Plus, tether.Options{Auth: Session, Auths: []tether.Auth{Session, Key}}))
	mux.Handle(tether.Route("GET /loose", Loose, tether.Options{Auths: both}))
	mux.Handle(tether.Route("GET /free", Free, tether.Options{Public: true, Auths: []tether.Auth{Session, Key}}))
}
