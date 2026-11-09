package tether

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// carrier names where the credential travels, to tell two Auths apart. A
// bearer token travels in the Authorization header, so an Auth that reads
// that header by name cannot sit beside it.
func (a Auth) carrier() string {
	if a.Cookie != "" {
		return "the cookie " + a.Cookie
	}
	name := "Authorization"
	if a.Header != "" {
		name = http.CanonicalHeaderKey(a.Header)
	}
	return "the header " + name
}

// carried reports whether r brings a credential where a expects it. It
// says nothing about whether the credential is good; that is the Check's.
func (a Auth) carried(r *http.Request) bool {
	switch {
	case a.Cookie != "":
		c, err := r.Cookie(a.Cookie)
		return err == nil && c.Value != ""
	case a.Header != "":
		return strings.TrimSpace(r.Header.Get(a.Header)) != ""
	}
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	return ok && strings.EqualFold(scheme, "Bearer") && strings.TrimSpace(token) != ""
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
//		reader   = tether.Options{Auths: []tether.Auth{Session, APIKey}, Scopes: []string{"catalog:read"}}
//	)
//
//	mux.Handle(tether.Route("GET /v1/me", GetMe, signedIn))
//
// Write them in the call or as package variables initialised with a
// literal, so the generator can read them. Once one route of a program
// has an Auth, every route says who may call it: Auth, Auths, or Public.
type Options struct {
	// Auth is how the caller must prove who it is.
	Auth Auth
	// Auths are several ways in, for a route that browsers and other
	// systems call alike; Auth is the one way in. tether calls the Check of
	// the first whose credential the request carries, and the first Check
	// when it carries none. A Check that fails answers the request: tether
	// does not try the next.
	Auths []Auth
	// Scopes are what the caller must be allowed, handed to the Check:
	// roles of a session, scopes of a key. With Auths every Check enforces
	// them, each in its own terms.
	Scopes []string
	// Public says the route is open to anyone, in a program whose other
	// routes need an Auth.
	Public bool
}

// auths lists the ways in, none for a route anyone may call.
func (o Options) auths() []Auth {
	if o.Auth.set() {
		return []Auth{o.Auth}
	}
	return o.Auths
}

func (o Options) valid() error {
	switch {
	case o.Auth.set() && o.Auths != nil:
		return errors.New("a route has an Auth or Auths, not both")
	case o.Auths != nil && len(o.Auths) < 2:
		return errors.New("Auths are alternatives and need two or more; for one way in, use Auth")
	case o.Public && len(o.auths()) > 0:
		return errors.New("a route is either Public or has an Auth, not both")
	case len(o.Scopes) > 0 && len(o.auths()) == 0:
		return errors.New("Scopes need an Auth that checks them")
	}
	seen := map[string]bool{}
	for _, a := range o.auths() {
		if err := a.valid(); err != nil {
			return err
		}
		c := a.carrier()
		if seen[c] {
			// tether picks the Auth by where the credential is.
			return fmt.Errorf("two Auths read %s; tether could not tell which one a request carries", c)
		}
		seen[c] = true
	}
	return nil
}
