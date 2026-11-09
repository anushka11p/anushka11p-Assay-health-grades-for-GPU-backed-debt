package report

import (
	"encoding/json"
	"testing"
)

func TestParseKeepsBigIntegersExact(t *testing.T) {
	// 2^53 + 1 can't be represented as a float64; a plain json.Unmarshal would round it.
	in := `{"schema_version":1,"gpus":[{"probes":[{"name":"memtest","result":{"offset":9007199254740993}}]}]}`
	r, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	got := r.GPUs[0].Probes[0].Result["offset"]
	if n, ok := got.(json.Number); !ok || n.String() != "9007199254740993" {
		t.Fatalf("got %v (%T)", got, got)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("want error")
	}
}
