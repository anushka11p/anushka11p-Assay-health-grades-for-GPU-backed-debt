package report

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Parse reads a report from JSON.
//
// Numbers inside probe results are kept exactly as written (json.Number), not
// converted to float64. A float64 can't hold every 64-bit integer, and a single
// changed digit would break the signature.
func Parse(data []byte) (Report, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var r Report
	if err := dec.Decode(&r); err != nil {
		return Report{}, fmt.Errorf("not a valid report: %w", err)
	}
	return r, nil
}
