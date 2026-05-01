package http

import "encoding/json"

// jsonMarshal is a thin wrapper over encoding/json so request.go does not
// import encoding/json directly — keeps the file's import set narrow and
// makes a future replacement (e.g. with a streaming encoder) localised.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
