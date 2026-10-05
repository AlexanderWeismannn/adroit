package theme

import "encoding/json"

// jsonUnmarshal exists so Pair.UnmarshalJSON can decode without importing
// encoding/json into a file that also defines an unmarshaller for it, which
// reads as though it might recurse.
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
