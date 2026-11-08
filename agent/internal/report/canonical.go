package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Canonical returns the one exact byte encoding of v that gets hashed and signed.
//
// The same data must always produce the same bytes, or signatures break. JSON allows
// many spellings of the same thing (key order, spaces, "1" vs "1.0"), so we pin it down:
//   - object keys sorted, no whitespace
//   - no HTML escaping (<, > and & are written as-is)
//   - integers only: a float anywhere is an error
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Decode into generic maps/slices, keeping numbers as their original text.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, tree, "$"); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeCanonical writes one JSON value. path (like "$.gpus[0].samples") is only for error messages.
func writeCanonical(buf *bytes.Buffer, v any, path string) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return fmt.Errorf("canonical JSON: float %s at %s (use integer units)", x, path)
		}
		buf.WriteString(string(x))
	case string:
		writeString(buf, x)
	case []any:
		buf.WriteByte('[')
		for i, el := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, el, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k], path+"."+k); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("canonical JSON: unexpected type %T at %s", v, path)
	}
	return nil
}

// writeString writes a JSON string without HTML escaping.
func writeString(buf *bytes.Buffer, s string) {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	buf.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
}
