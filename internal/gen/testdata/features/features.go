// Package features exercises what the generator supports; its output is
// compared byte for byte with the files in want.
package features

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/maulai/tether"
	"github.com/maulai/tether/internal/gen/testdata/features/catalog"
	legacycatalog "github.com/maulai/tether/internal/gen/testdata/features/legacy/catalog"
	"github.com/maulai/tether/messages/de"
)

func init() {
	tether.Pattern("sku", `[A-Z]{3}-\d{4}`, "Use a code like ABC-1234.")
	tether.Pattern("zip", `\d{5}`, "Use five digits.") // replaced by Messages below
	tether.Check("ean13", func(s string) bool { return len(s) == 13 }, "Not a valid EAN-13.")
	tether.Format[ItemID]("itemID", `[0-9a-f]{8}`, "Use eight hex digits.")
	tether.Messages(de.Messages, map[string]string{"required": "Bitte ausfüllen.", "zip": "Bitte fünf Ziffern."})
}

// Kind is the kind of an item.
type Kind string

const (
	KindNone    Kind = ""
	KindProduct Kind = "product"
	KindService Kind = "service"
)

// ItemID names an item; it reads itself from eight hex digits.
type ItemID [4]byte

func (id *ItemID) UnmarshalText(b []byte) error {
	if hex.DecodedLen(len(b)) != len(id) {
		return errors.New("not an item id")
	}
	_, err := hex.Decode(id[:], b)
	return err
}

func (id ItemID) MarshalText() ([]byte, error) { return []byte(hex.EncodeToString(id[:])), nil }

// ErrSoldOut: the item cannot be ordered now. The client recognises it
// with isSoldOut.
var ErrSoldOut = tether.Problem{Status: http.StatusConflict, Code: "soldOut", Message: "Sold out."}

var errLocked = tether.Problem{Status: http.StatusLocked, Code: "itemLocked", Message: "The item is locked."}

// Session is the sign-in of the browser.
var Session = tether.Auth{Cookie: "session", Check: check}

// Key is the key of another system.
var Key = tether.Auth{Bearer: true, Check: check}

func check(r *http.Request, scopes []string) (*http.Request, error) { return r, nil }

var (
	open     = tether.Options{Public: true}
	signedIn = tether.Options{Auth: Session}
	admin    = tether.Options{Auth: Session, Scopes: []string{"admin"}}
)

// Page is one page of a list.
type Page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

// Audit is embedded and lies flat in JSON.
type Audit struct {
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// Item is what the API returns.
type Item struct {
	ID    string            `json:"id"`
	Kind  Kind              `json:"kind"`
	Price float64           `json:"price"`
	Stock int32             `json:"stock"`
	Big   int64             `json:"big,string"`
	Data  json.RawMessage   `json:"data,omitempty"`
	Attrs map[string]string `json:"attrs"`
	Audit
	secret string
}

// ItemInput creates an item.
type ItemInput struct {
	// SKU identifies the item.
	SKU     string              `json:"sku" v:"required,sku" example:"ABC-1234"`
	EAN     string              `json:"ean" v:"ean13"`
	Kind    Kind                `json:"kind" v:"required"`
	Price   *float64            `json:"price" v:"requiredif=kind:product,gt=0"`
	Count   uint8               `json:"count" v:"max=100"`
	Labels  map[string]string   `json:"labels" v:"dive,max=40"`
	Stock   map[Kind]int        `json:"stock"`
	Lines   []Line              `json:"lines" v:"min=1"`
	Size    string              `json:"size" v:"oneof=s m l"`
	Parent  *ItemID             `json:"parent"`
	Color   catalog.Color       `json:"color"`
	Grade   legacycatalog.Grade `json:"grade"`
	Contact struct {
		Email string `json:"email" v:"email"`
		Site  string `json:"site" v:"url"`
	} `json:"contact"`
	Ignored string `json:"-"`
}

// Line is one line of an item.
type Line struct {
	Text  string `json:"text" v:"required,max=200"`
	Qty   int    `json:"qty" v:"min=1"`
	Child *Line  `json:"child"`
}

// Filter narrows the list.
type Filter struct {
	Kind  Kind      `json:"kind"`
	Tags  []string  `json:"tags"`
	Since time.Time `json:"since"`
	Limit *int      `json:"limit" v:"max=100"`
	Page  int       `json:"page" v:"min=1"`
}

// Settings are replaced as a whole with PUT, so every field must be sent,
// in TypeScript and to the server.
type Settings struct {
	Name   string            `json:"name" v:"required"`
	Notes  string            `json:"notes"`
	Hours  []Opening         `json:"hours"`
	Labels map[string]string `json:"labels"`
	// Logo may be left out, and then stays as it is.
	Logo string `json:"logo,omitzero"`
}

// Opening is one opening time. Inside a PUT input it is complete too.
type Opening struct {
	Day   string `json:"day" v:"required"`
	Hours string `json:"hours"`
}

// Reason says why stock changed.
type Reason string

const (
	ReasonSale   Reason = "sale"
	ReasonReturn Reason = "return"
)

// Adjustment never passes a route. Book checks it with tether.Validate, so
// its enum is registered all the same, and it stays out of the API.
type Adjustment struct {
	Reason Reason `json:"reason" v:"required"`
	Qty    int    `json:"qty" v:"min=1"`
}

// Book records an adjustment that does not arrive over HTTP.
func Book(a Adjustment) error { return tether.Validate(a) }

type store struct{}

// List returns a page of items.
func (s *store) List(r *http.Request, f Filter) (Page[Item], error) { return Page[Item]{}, nil }

// CreateItem adds an item.
func CreateItem(r *http.Request, in ItemInput) (*Item, error) { return nil, ErrSoldOut }

// UploadImage stores an image for an item.
func UploadImage(r *http.Request, id ItemID, f tether.Upload) (Item, error) { return Item{}, nil }

// Sheet renders the data sheet of an item as PDF.
func Sheet(r *http.Request, id ItemID) (tether.File, error) { return tether.File{}, nil }

// ServeAsset serves a static file below /assets/.
func ServeAsset(r *http.Request, path string) (tether.File, error) { return tether.File{}, nil }

// Archive archives an item.
func Archive(r *http.Request, _ ItemID) error { return errLocked }

// StockOf returns how many items of a kind lie on shelf n.
func StockOf(r *http.Request, kind Kind, n int) (int, error) { return 0, nil }

// ReplaceSettings replaces the shop's settings.
func ReplaceSettings(r *http.Request, in Settings) (Settings, error) { return in, nil }

// Routes registers the API.
func Routes(mux *http.ServeMux) {
	s := &store{}
	mux.Handle(tether.Route("GET /api/items", s.List, open))
	mux.Handle(tether.Route("POST /api/items", CreateItem, signedIn))
	mux.Handle(tether.Route("POST /api/items/{id}/image", UploadImage, signedIn))
	mux.Handle(tether.Route("GET /api/items/{id}/sheet", Sheet, tether.Options{Auth: Key, Scopes: []string{"items:read"}}))
	mux.Handle(tether.Route("GET /assets/{path...}", ServeAsset, open))
	mux.Handle(tether.Route("PATCH /api/items/{id}/archive", Archive, admin))
	mux.Handle(tether.Route("GET /api/stock/{kind}/{n}", StockOf, open))
	mux.Handle(tether.Route("PUT /api/settings", ReplaceSettings, admin))
}
