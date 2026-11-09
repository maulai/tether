package tether

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type orderLine struct {
	SKU  string `json:"sku"`
	Note string `json:"note"`
}

type orderAudit struct {
	By string `json:"by"`
}

type order struct {
	orderAudit                      // embedded: its fields are the order's own
	Name       string               `json:"name"`
	Lines      []orderLine          `json:"lines"`
	Labels     map[string]orderLine `json:"labels"`
	Ref        *orderLine           `json:"ref"`
	Secret     string               `json:"secret,omitzero"` // may be left out
	Skip       string               `json:"-"`
}

func replaceOrder(r *http.Request, in order) error { return nil }

// fieldRules lists the errors of a problem as field:rule.
func fieldRules(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var got []string
	for _, e := range decodeProblem(t, rec).Errors {
		got = append(got, e.Field+":"+e.Rule)
	}
	return strings.Join(got, " ")
}

// A PUT replaces the whole record, so a field it lacks is an error, at any
// depth, instead of a value cleared without notice.
func TestPutNeedsEveryField(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"zzz": 1}`, "by:missing labels:missing lines:missing name:missing ref:missing zzz:unknown"},
		{``, "by:missing labels:missing lines:missing name:missing ref:missing"},
		{`null`, "by:missing labels:missing lines:missing name:missing ref:missing"},
		{`{"by": "x", "name": "a", "lines": [{"sku": "1"}], "labels": {"de": {"note": "n"}}, "ref": null}`, "labels.de.sku:missing lines.0.note:missing"},
	}
	for _, c := range cases {
		rec := serve(t, "PUT /orders", replaceOrder, httptest.NewRequest("PUT", "/orders", strings.NewReader(c.body)))
		if got := fieldRules(t, rec); rec.Code != 422 || got != c.want {
			t.Errorf("%s: status %d, errors %s, want %s", c.body, rec.Code, got, c.want)
		}
	}
	full := `{"by": "x", "name": "a", "lines": [], "labels": {}, "ref": {"sku": "1", "note": ""}}`
	if rec := serve(t, "PUT /orders", replaceOrder, httptest.NewRequest("PUT", "/orders", strings.NewReader(full))); rec.Code != 204 {
		t.Errorf("complete body: status %d, body %s", rec.Code, rec.Body)
	}
	// POST and PATCH still take what they get.
	for _, method := range []string{"POST", "PATCH"} {
		rec := serve(t, method+" /orders", replaceOrder, httptest.NewRequest(method, "/orders", strings.NewReader(`{}`)))
		if rec.Code != 204 {
			t.Errorf("%s: status %d, body %s", method, rec.Code, rec.Body)
		}
	}
}
