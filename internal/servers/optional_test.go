package servers

import (
	"encoding/json"
	"testing"
)

func TestOptionalDistinguishesAbsentNullAndValue(t *testing.T) {
	type body struct {
		A optional[string] `json:"a"`
		B optional[string] `json:"b"`
		C optional[string] `json:"c"`
	}
	var b body
	if err := json.Unmarshal([]byte(`{"b":null,"c":"x"}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.A.Set || b.A.Value != nil {
		t.Errorf("absent: %+v", b.A)
	}
	if !b.B.Set || b.B.Value != nil {
		t.Errorf("null: %+v", b.B)
	}
	if !b.C.Set || b.C.Value == nil || *b.C.Value != "x" {
		t.Errorf("value: %+v", b.C)
	}
}

func TestOptionalRejectsUnknownNestedFields(t *testing.T) {
	type inner struct {
		X int `json:"x"`
	}
	var o optional[inner]
	if err := json.Unmarshal([]byte(`{"x":1,"y":2}`), &o); err == nil {
		t.Fatal("unknown nested field accepted")
	}
	if err := json.Unmarshal([]byte(`{"x":"1"}`), &o); err == nil {
		t.Fatal("wrong type accepted")
	}
}
