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

func Routes(mux *http.ServeMux) {
	mux.Handle(tether.Route("GET /list", List, tether.Options{Auth: Session}))
	mux.Handle(tether.Route("GET /open", Open))
	mux.Handle(tether.Route("GET /mixed", Mixed, tether.Options{Auth: Session, Public: true}))
	mux.Handle(tether.Route("GET /scoped", Scoped, tether.Options{Scopes: []string{"admin"}}))
	mux.Handle(tether.Route("GET /inline", Inline, tether.Options{Auth: tether.Auth{Bearer: true, Check: check}}))
	mux.Handle(tether.Route("GET /called", Called, options()))
	mux.Handle(tether.Route("GET /double", Double, tether.Options{Auth: Both}))
}
