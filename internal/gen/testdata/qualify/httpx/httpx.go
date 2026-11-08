// Package httpx holds a generic page type.
package httpx

// Page is one page of a list.
type Page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}
