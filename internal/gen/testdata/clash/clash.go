// Package clash uses two packages of the same name that both contribute a
// type, which -qualify cannot tell apart.
package clash

import (
	"net/http"

	"github.com/maulai/tether"
	atax "github.com/maulai/tether/internal/gen/testdata/clash/a/tax"
	btax "github.com/maulai/tether/internal/gen/testdata/clash/b/tax"
)

// GetA returns a rate.
func GetA(r *http.Request) (atax.Rate, error) { return atax.Rate{}, nil }

// GetB returns a rate.
func GetB(r *http.Request) (btax.Rate, error) { return btax.Rate{}, nil }

// Routes registers the API.
func Routes(mux *http.ServeMux) {
	mux.Handle(tether.Route("GET /a", GetA))
	mux.Handle(tether.Route("GET /b", GetB))
}
