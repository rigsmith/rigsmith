package peek

import "encoding/json"

// jsonUnmarshal is encoding/json behind a name, so the header readers above do
// not each import it for one call.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
