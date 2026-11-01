// Package mistakes is full of errors the generator must report; the
// expected report is want/errors.txt.
package mistakes

import (
	"net/http"

	"github.com/maulai/tether"
)

type Input struct {
	Count  int    `json:"count" v:"required"`
	Name   string `json:"name" v:"required,bogus"`
	Rate   string `json:"rate" v:"gt=0"`
	When   string `json:"when" v:"requiredif=missing:x"`
	Flag   bool   `json:"flag"`
	Note   string `json:"note" v:"requiredif=flag:yes"`
	Things []int  `json:"things" v:"dive,email"`
}

type Filter struct {
	Nested struct{ A string } `json:"nested"`
}

type Out struct {
	Ch chan int `json:"ch"`
}

func Create(r *http.Request, in Input) error   { return nil }
func Delete(r *http.Request) error             { return nil }
func Search(r *http.Request, f Filter) error   { return nil }
func Broken(r *http.Request) (Out, error)      { return Out{}, nil }
func WrongShape(in Input) error                { return nil }
func Pointer(r *http.Request, in *Input) error { return nil }

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
}
