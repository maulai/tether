package tether

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type paint struct {
	Color color  `json:"color"`
	Note  string `json:"note" v:"max=3"`
}

func TestValidate(t *testing.T) {
	if err := Validate(&paint{Color: "red", Note: "ok"}); err != nil {
		t.Errorf("valid: %v", err)
	}
	// The same rules and enums as a route's input, in the same order.
	got := Issues(Validate(paint{Color: "pink", Note: "long"}))
	want := []Issue{
		{Field: "color", Rule: "enum", Message: "Must be one of the offered values."},
		{Field: "note", Rule: "maxLength", Message: "Must be at most 3 characters."},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("issues %+v", got)
	}
	// A handler returns the error as it is and answers 422.
	h := func(r *http.Request) error { return Validate(paint{Note: "long"}) }
	if rec := serve(t, "POST /x", h, httptest.NewRequest("POST", "/x", nil)); rec.Code != 422 || fieldRules(t, rec) != "note:maxLength" {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
	defer func() {
		if recover() == nil {
			t.Error("Validate of a non-struct did not panic")
		}
	}()
	Validate(42)
}

func TestIssues(t *testing.T) {
	if Issues(nil) != nil || Issues(errors.New("disk full")) != nil {
		t.Error("issues in an error without field errors")
	}
	err := errors.Join(FieldError("a", "x"), errors.New("other"), FieldError("b", "y"))
	if got := Issues(err); len(got) != 2 || got[0].Field != "a" || got[1].Message != "y" {
		t.Errorf("issues %+v", got)
	}
}
