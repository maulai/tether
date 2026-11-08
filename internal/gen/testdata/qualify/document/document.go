// Package document has a Status, as package stock does.
package document

// Status is where a document stands.
type Status string

const (
	StatusDraft  Status = "draft"
	StatusIssued Status = "issued"
)

// Document is named like its package, so it stays Document.
type Document struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
}
