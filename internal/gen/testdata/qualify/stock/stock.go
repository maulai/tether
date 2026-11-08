// Package stock has a Status, as package document does.
package stock

// Status is whether an item is in stock.
type Status string

const (
	StatusIn  Status = "in"
	StatusOut Status = "out"
)

// Item is an item in stock.
type Item struct {
	SKU    string `json:"sku"`
	Status Status `json:"status"`
}
