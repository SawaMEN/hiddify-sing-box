package finalmask

import (
	"bytes"
	"encoding/json"
)

// hInt32Ranges keeps the compact panel form compatible with Xray's array form.
// finalmask uses the standard JSON loader, so this implements its unmarshaler.
type hInt32Ranges []Int32Range

func (r *hInt32Ranges) UnmarshalJSON(content []byte) error {
	trimmed := bytes.TrimSpace(content)
	if bytes.Equal(trimmed, []byte("null")) {
		*r = nil
		return nil
	}
	if bytes.HasPrefix(trimmed, []byte("[")) {
		return json.Unmarshal(content, (*[]Int32Range)(r))
	}
	var single Int32Range
	if err := json.Unmarshal(content, &single); err != nil {
		return err
	}
	*r = hInt32Ranges{single}
	return nil
}
