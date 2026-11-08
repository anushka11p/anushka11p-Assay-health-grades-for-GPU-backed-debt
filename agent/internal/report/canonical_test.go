package report

import (
	"strings"
	"testing"
)

func TestCanonicalSortsKeysAndDropsWhitespace(t *testing.T) {
	in := map[string]any{"b": 1, "a": []any{true, nil, "x"}, "c": map[string]any{"z": 2, "y": 3}}
	got, err := Canonical(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[true,null,"x"],"b":1,"c":{"y":3,"z":2}}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCanonicalIsStableAcrossKeyOrder(t *testing.T) {
	a, _ := Canonical(map[string]any{"x": 1, "y": 2})
	b, _ := Canonical(map[string]any{"y": 2, "x": 1})
	if string(a) != string(b) {
		t.Fatalf("same data, different bytes: %s vs %s", a, b)
	}
}

func TestCanonicalRejectsFloats(t *testing.T) {
	_, err := Canonical(map[string]any{"gpus": []any{map[string]any{"power_w": 61.5}}})
	if err == nil || !strings.Contains(err.Error(), "$.gpus[0].power_w") {
		t.Fatalf("want float error naming the path, got %v", err)
	}
}

func TestCanonicalDoesNotEscapeHTML(t *testing.T) {
	got, _ := Canonical(map[string]any{"s": "a<b & c>d"})
	if string(got) != `{"s":"a<b & c>d"}` {
		t.Fatalf("got %s", got)
	}
}

func TestCanonicalKeepsNullForUnknownValues(t *testing.T) {
	got, err := Canonical(ECCCounts{Corrected: Ptr(uint64(0)), Uncorrected: nil})
	if err != nil {
		t.Fatal(err)
	}
	// 0 means "checked, zero errors"; null means "could not check". Both must survive.
	if string(got) != `{"corrected":0,"uncorrected":null}` {
		t.Fatalf("got %s", got)
	}
}

func TestCanonicalHandlesLargeUint64(t *testing.T) {
	// Lifetime counters can exceed 2^53, where float64 would silently lose precision.
	got, _ := Canonical(ECCCounts{Corrected: Ptr(uint64(18446744073709551615))})
	if !strings.Contains(string(got), "18446744073709551615") {
		t.Fatalf("lost precision: %s", got)
	}
}
