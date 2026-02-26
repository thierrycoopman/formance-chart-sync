package convert

import (
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// ToJSON parses rawYAML — including anchors, aliases, merge keys,
// multi-line scalars, and all YAML 1.2 features — and returns canonical
// JSON bytes. The YAML document is fully resolved before conversion;
// anchors and merge keys do not appear in the output.
func ToJSON(rawYAML []byte) ([]byte, error) {
	var raw any
	if err := yaml.Unmarshal(rawYAML, &raw); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	normalised, err := normalise(raw)
	if err != nil {
		return nil, fmt.Errorf("normalising YAML value tree: %w", err)
	}

	out, err := json.Marshal(normalised)
	if err != nil {
		return nil, fmt.Errorf("marshalling to JSON: %w", err)
	}
	return out, nil
}

// normalise recursively walks the value tree produced by yaml.v3 and converts
// any types that encoding/json cannot serialise.
func normalise(v any) (any, error) {
	switch val := v.(type) {

	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			norm, err := normalise(child)
			if err != nil {
				return nil, err
			}
			out[k] = norm
		}
		return out, nil

	// yaml.v2 legacy type — yaml.v3 should not produce this, but guard anyway.
	case map[any]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			norm, err := normalise(child)
			if err != nil {
				return nil, err
			}
			out[fmt.Sprintf("%v", k)] = norm
		}
		return out, nil

	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			norm, err := normalise(child)
			if err != nil {
				return nil, err
			}
			out[i] = norm
		}
		return out, nil

	case time.Time:
		return val.UTC().Format(time.RFC3339), nil

	case []byte:
		// Binary data — encoding/json handles []byte -> base64 natively.
		return val, nil

	default:
		// bool, int, int64, float64, string, nil — all JSON-native.
		return val, nil
	}
}
