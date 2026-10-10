package servers

import (
	"bytes"
	"encoding/json"
)

// optional is a JSON field that distinguishes "absent" (Set == false), "null" (Set, Value ==
// nil) and a value (Set, Value != nil). Unknown fields inside the value are rejected, like
// httpx.DecodeJSON does for the outer object.
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	o.Value = nil
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}
