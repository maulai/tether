package tether

import (
	"errors"
	"net/http"
)

// Auth is one way a caller proves who it is: where the credential travels,
// for the OpenAPI document, and the project's check of it, which tether
// calls before it reads a route's input. Declare it once as a package
// variable; its name names it in OpenAPI, its doc comment describes it.
//
//	// Session is the sign-in of the browser: a session cookie and a CSRF header.
//	var Session = tether.Auth{Cookie: "session_token", Check: checkSession}
//
// Exactly one of Cookie, Header and Bearer says where the credential is.
type Auth struct {
	Cookie string // the credential is the cookie of this name
	Header string // the credential is the header of this name
	Bearer bool   // the credential is Authorization: Bearer <token>
	// Check verifies the credential of r for a route that needs scopes,
	// which are empty when the route names none. It returns r with what the
	// handler needs in its context, such as the account, derived from r with
	// r.WithContext; nil means r as it is. An error answers the request like
	// a handler's error, before anything else runs: return a Problem with a
	// code, so the client can tell "signed out" from "not allowed".
	Check func(r *http.Request, scopes []string) (*http.Request, error)
}

func (a Auth) set() bool {
	return a.Cookie != "" || a.Header != "" || a.Bearer || a.Check != nil
}

func (a Auth) valid() error {
	n := 0
	for _, ok := range []bool{a.Cookie != "", a.Header != "", a.Bearer} {
		if ok {
			n++
		}
	}
	switch {
	case n != 1:
		return errors.New("an Auth names exactly one of Cookie, Header and Bearer")
	case a.Check == nil:
		return errors.New("an Auth needs a Check")
	}
	return nil
}

// Options are the settings of one route, the third argument of Route.
//
//	var (
//		open     = tether.Options{Public: true}
//		signedIn = tether.Options{Auth: Session}
//		admin    = tether.Options{Auth: Session, Scopes: []string{"admin"}}
//	)
//
//	mux.Handle(tether.Route("GET /v1/me", GetMe, signedIn))
//
// Write them in the call or as package variables initialised with a
// literal, so the generator can read them. Once one route of a program
// has an Auth, every route says who may call it: Auth, or Public.
type Options struct {
	// Auth is how the caller must prove who it is.
	Auth Auth
	// Scopes are what the caller must be allowed, handed to Auth.Check:
	// roles of a session, scopes of a key.
	Scopes []string
	// Public says the route is open to anyone, in a program whose other
	// routes need an Auth.
	Public bool
}

func (o Options) valid() error {
	switch {
	case o.Public && o.Auth.set():
		return errors.New("a route is either Public or has an Auth, not both")
	case len(o.Scopes) > 0 && !o.Auth.set():
		return errors.New("Scopes need an Auth that checks them")
	case o.Auth.set():
		return o.Auth.valid()
	}
	return nil
}
