// Package mistakes is full of errors the generator must report; the
// expected report is want/errors.txt.
package mistakes

import (
	"errors"
	"net/http"

	"github.com/maulai/tether"
	"github.com/maulai/tether/internal/gen/testdata/mistakes/hidden"
)

type Input struct {
	Count  int          `json:"count" v:"required"`
	Name   string       `json:"name" v:"required,bogus"`
	Rate   string       `json:"rate" v:"gt=0"`
	When   string       `json:"when" v:"requiredif=missing:x"`
	Flag   bool         `json:"flag"`
	Note   string       `json:"note" v:"requiredif=flag:yes"`
	Things []int        `json:"things" v:"dive,email"`
	Level  hidden.Level `json:"level"`
	Tokens []Token      `json:"tokens"`
}

// Token reads itself with UnmarshalText but has no Format, so the browser
// could not check it.
type Token [2]byte

func (t *Token) UnmarshalText(b []byte) error {
	if len(b) != 2 {
		return errors.New("not a token")
	}
	return nil
}

func (t Token) MarshalText() ([]byte, error) { return t[:], nil }

// Query has a field named like the path parameter of its route.
type Query struct {
	Q string `json:"q"`
}

type Filter struct {
	Nested struct{ A string } `json:"nested"`
}

type Out struct {
	Ch chan int `json:"ch"`
}

func Create(r *http.Request, in Input) error   { return nil }
func Delete(r *http.Request, id string) error  { return nil }
func Search(r *http.Request, f Filter) error   { return nil }
func Broken(r *http.Request) (Out, error)      { return Out{}, nil }
func WrongShape(in Input) error                { return nil }
func Pointer(r *http.Request, in *Input) error { return nil }

func NoParam(r *http.Request) error                      { return nil }
func WrongName(r *http.Request, key string) error        { return nil }
func BadPath(r *http.Request, id []string) error         { return nil }
func Rest(r *http.Request, p int) error                  { return nil }
func ByToken(r *http.Request, tok Token) error           { return nil }
func Find(r *http.Request, q string, f Query) error      { return nil }
func InputFirst(r *http.Request, in Input, id int) error { return nil }

var pattern = "GET /dynamic"

func Routes(mux *http.ServeMux) {
	mux.Handle(tether.Route("POST /a", Create))
	mux.Handle(tether.Route("DELETE /a/{id}", Delete))
	mux.Handle(tether.Route("GET /search", Search))
	mux.Handle(tether.Route("GET /broken", Broken))
	mux.Handle(tether.Route("GET /shape", WrongShape))
	mux.Handle(tether.Route("POST /pointer", Pointer))
	mux.Handle(tether.Route(pattern, Delete))
	mux.Handle(tether.Route("POST /closure", func(r *http.Request) error { return nil }))
	mux.Handle(tether.Route("FETCH /x", Delete))
	mux.Handle(tether.Route("GET /b/{id}", NoParam))
	mux.Handle(tether.Route("GET /c/{id}", WrongName))
	mux.Handle(tether.Route("GET /d/{id}", BadPath))
	mux.Handle(tether.Route("GET /e/{p...}", Rest))
	mux.Handle(tether.Route("GET /f/{tok}", ByToken))
	mux.Handle(tether.Route("GET /g/{q}", Find))
	mux.Handle(tether.Route("PUT /h/{id}", InputFirst))
}

var (
	errBadCode = tether.Problem{Status: http.StatusForbidden, Code: "Bad-Code", Message: "x"}
	errOkay    = tether.Problem{Status: http.StatusOK, Code: "okay", Message: "x"}
	errOnce    = tether.Problem{Status: http.StatusConflict, Code: "twice", Message: "x"}
	errTwice   = tether.Problem{Status: http.StatusConflict, Code: "twice", Message: "y"}
)

func init() {
	tether.Pattern("blank", `x`, "")
}

// Count is checked outside a route, with the same checks as an input.
type Count struct {
	N int `json:"n" v:"required"`
}

func checkCount(c Count) error { return tether.Validate(c) }

func checkAny(v any) error { return tether.Validate(v) }
