// Package tax declares types whose names the generator prefixes with -qualify.
package tax

// Class is the tax class of a rate.
type Class string

const (
	ClassStandard Class = "standard"
	ClassReduced  Class = "reduced"
)

// VatRate is a rate as the API returns it.
type VatRate struct {
	Class   Class    `json:"class"`
	Percent float64  `json:"percent"`
	Rule    TaxRule  `json:"rule"`
	Tree    Taxonomy `json:"tree"`
}

// VatRateInput creates a rate.
type VatRateInput struct {
	Class   Class   `json:"class" v:"required"`
	Percent float64 `json:"percent" v:"min=0"`
}

// TaxRule starts with the package name as a word, so it keeps its name.
type TaxRule struct {
	Name string `json:"name"`
}

// Taxonomy starts with "Tax" but not as a word, so it gets the prefix.
type Taxonomy struct {
	Path string `json:"path"`
}
