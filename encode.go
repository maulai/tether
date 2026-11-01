package tether

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"time"
)

// File is a handler output sent as raw bytes instead of JSON, such as a PDF
// or a CSV export.
//
// For GET and HEAD, a Body that is an io.ReadSeeker is served with
// http.ServeContent: Range requests get 206 with only the requested bytes,
// and If-Range, If-Modified-Since (with ModTime) and If-None-Match (with an
// ETag set through ResponseHeader) are answered as the standard library
// does. Any other body is streamed whole.
type File struct {
	Name        string    // offered to the browser as the file name
	ContentType string    // defaults to application/octet-stream
	Body        io.Reader // closed after writing if it is an io.Closer
	// ModTime enables Last-Modified and If-Modified-Since; zero leaves them out.
	ModTime time.Time
	// Attachment asks the browser to download the file instead of showing it.
	Attachment bool
}

// Upload is a handler input read from a multipart form with one file in the
// part "file". The body is streamed, not buffered, and is valid only until
// the handler returns.
type Upload struct {
	Name        string
	ContentType string
	Body        io.Reader
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, v reflect.Value) {
	body, err := json.Marshal(normalize(v).Interface())
	if err != nil {
		internal(w, r, http.StatusInternalServerError, err)
		return
	}
	body = append(body, '\n')
	own(w.Header(), "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	w.Write(body)
}

func writeFile(w http.ResponseWriter, r *http.Request, f File) {
	if c, ok := f.Body.(io.Closer); ok {
		defer c.Close()
	}
	ct := f.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	own(w.Header(), ct)
	// inline unless asked otherwise: a PDF opens in the browser, which can
	// still save it.
	disposition := "inline"
	if f.Attachment {
		disposition = "attachment"
	}
	if f.Name != "" {
		if v := mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}); v != "" {
			w.Header().Set("Content-Disposition", v)
		} else {
			w.Header().Set("Content-Disposition", disposition)
		}
	} else if f.Attachment {
		w.Header().Set("Content-Disposition", disposition)
	}
	if rs, ok := f.Body.(io.ReadSeeker); ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		http.ServeContent(w, r, f.Name, f.ModTime, rs)
		return
	}
	w.WriteHeader(http.StatusOK)
	if f.Body != nil {
		io.Copy(w, f.Body)
	}
}

// normalize returns v with every nil slice replaced by an empty one and
// every nil map by an empty map. encoding/json writes nil as null, and the
// generated types promise a list, not "a list or null". Types with their
// own MarshalJSON are left alone.
func normalize(v reflect.Value) reflect.Value {
	if !v.IsValid() || !needsNormalizing(v.Type()) {
		return v
	}
	t := v.Type()
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(normalize(v.Elem()))
		return p
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(t).Elem()
		out.Set(normalize(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(t).Elem()
		out.Set(v)
		for i := range t.NumField() {
			if f := out.Field(i); f.CanSet() {
				f.Set(normalize(v.Field(i)))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.MakeSlice(t, 0, 0)
		}
		out := reflect.MakeSlice(t, v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(normalize(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(t).Elem()
		for i := range v.Len() {
			out.Index(i).Set(normalize(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.MakeMap(t)
		}
		out := reflect.MakeMapWithSize(t, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), normalize(iter.Value()))
		}
		return out
	}
	return v
}

var normCache sync.Map // reflect.Type -> bool

// needsNormalizing reports whether a value of t can contain a nil slice or
// map, so normalize can skip the copy for everything else.
func needsNormalizing(t reflect.Type) bool {
	if b, ok := normCache.Load(t); ok {
		return b.(bool)
	}
	normCache.Store(t, false) // breaks cycles in recursive types
	b := computeNeeds(t)
	normCache.Store(t, b)
	return b
}

func computeNeeds(t reflect.Type) bool {
	if t.Implements(jsonMarshaler) || t.Implements(textMarshaler) {
		return false
	}
	switch t.Kind() {
	case reflect.Slice:
		return true
	case reflect.Map:
		return true
	case reflect.Pointer, reflect.Array:
		return needsNormalizing(t.Elem())
	case reflect.Interface:
		return true
	case reflect.Struct:
		for i := range t.NumField() {
			if t.Field(i).IsExported() && needsNormalizing(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}
