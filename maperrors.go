package tether

import "sync"

var (
	mapMu  sync.RWMutex
	mapper func(error) error
)

// MapErrors registers a function every error a handler returns passes
// through before tether answers it, for errors of packages the project
// cannot change: a driver's "no rows" becomes Fail(http.StatusNotFound, …),
// a unique violation a FieldError. Returning the error unchanged keeps
// tether's default; returning nil means the original error, so a mapping
// can never turn a failure into a success. tether's own decoding and
// validation errors do not pass through it.
//
// Call it once, at start-up; a second call panics, as with Messages.
func MapErrors(fn func(error) error) {
	mapMu.Lock()
	defer mapMu.Unlock()
	if mapper != nil {
		panic("tether: MapErrors called twice")
	}
	if fn == nil {
		panic("tether: MapErrors with a nil function")
	}
	mapper = fn
}

func mapError(err error) error {
	mapMu.RLock()
	fn := mapper
	mapMu.RUnlock()
	if fn == nil {
		return err
	}
	if mapped := fn(err); mapped != nil {
		return mapped
	}
	return err
}
