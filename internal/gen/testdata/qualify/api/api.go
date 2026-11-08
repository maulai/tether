// Package api uses types of several packages, two of them named Status;
// the generator runs over it with -qualify.
package api

import (
	"net/http"

	"github.com/maulai/tether"
	"github.com/maulai/tether/internal/gen/testdata/qualify/document"
	"github.com/maulai/tether/internal/gen/testdata/qualify/httpx"
	"github.com/maulai/tether/internal/gen/testdata/qualify/stock"
	"github.com/maulai/tether/internal/gen/testdata/qualify/tax"
)

// ListVatRates returns the rates.
func ListVatRates(r *http.Request) (httpx.Page[tax.VatRate], error) {
	return httpx.Page[tax.VatRate]{}, nil
}

// CreateVatRate adds a rate.
func CreateVatRate(r *http.Request, in tax.VatRateInput) (tax.VatRate, error) {
	return tax.VatRate{}, nil
}

// GetDocument returns a document.
func GetDocument(r *http.Request) (document.Document, error) { return document.Document{}, nil }

// GetStock returns an item in stock.
func GetStock(r *http.Request) (stock.Item, error) { return stock.Item{}, nil }

// Routes registers the API.
func Routes(mux *http.ServeMux) {
	mux.Handle(tether.Route("GET /v1/vat-rates", ListVatRates))
	mux.Handle(tether.Route("POST /v1/vat-rates", CreateVatRate))
	mux.Handle(tether.Route("GET /v1/documents/{id}", GetDocument))
	mux.Handle(tether.Route("GET /v1/stock/{sku}", GetStock))
}
