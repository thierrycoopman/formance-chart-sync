package chart

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/xeipuuv/gojsonschema"

	"github.com/formancehq/formance-chart-sync/internal/convert"
)

var compiledSchema *gojsonschema.Schema
var compiledLedgerSchema *gojsonschema.Schema

// LoadSchema compiles the JSON Schema at the given path. Must be called once
// before any call to Validate.
func LoadSchema(schemaPath string) error {
	loader := gojsonschema.NewReferenceLoader("file://" + schemaPath)
	var err error
	compiledSchema, err = gojsonschema.NewSchema(loader)
	return err
}

// LoadLedgerSchema compiles the Ledger API JSON Schema at the given path.
// Must be called once before any call to ValidateLedgerPayload.
func LoadLedgerSchema(schemaPath string) error {
	loader := gojsonschema.NewReferenceLoader("file://" + schemaPath)
	var err error
	compiledLedgerSchema, err = gojsonschema.NewSchema(loader)
	return err
}

// ValidateLedgerPayload validates extracted Ledger JSON against the
// V2SchemaData JSON Schema. This catches fields that the Ledger API
// does not accept before the push attempt.
func ValidateLedgerPayload(ledgerJSON []byte) error {
	if compiledLedgerSchema == nil {
		return errors.New("ledger schema not loaded — call LoadLedgerSchema first")
	}

	result, err := compiledLedgerSchema.Validate(gojsonschema.NewBytesLoader(ledgerJSON))
	if err != nil {
		return fmt.Errorf("running ledger schema validator: %w", err)
	}
	if result.Valid() {
		return nil
	}

	var errs []string
	for _, desc := range result.Errors() {
		errs = append(errs, desc.String())
	}
	return fmt.Errorf("ledger schema validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

// Result holds the outcome of validating a single chart file.
type Result struct {
	Valid  bool
	Errors []string
	JSON   []byte // resolved, normalised JSON — ready to push
}

func (r *Result) ErrorSummary() string {
	return strings.Join(r.Errors, "\n  - ")
}

// ExtractLedgerSchema extracts only the Ledger-compatible subset from the chart JSON.
// It keeps only chart, transactions, and queries fields, dropping business context,
// assets, rules, and other v4 schema extensions. This produces JSON ready for the
// Formance Ledger API.
//
// Supports both v4 flat format (chart, transactions at top level) and legacy
// Kubernetes wrapper format (apiVersion, kind, metadata, spec).
func (r *Result) ExtractLedgerSchema() ([]byte, error) {
	if !r.Valid || len(r.Errors) > 0 {
		return nil, errors.New("cannot extract from invalid chart")
	}

	var fullChart map[string]any
	if err := json.Unmarshal(r.JSON, &fullChart); err != nil {
		return nil, fmt.Errorf("parsing chart JSON: %w", err)
	}

	// Determine source: use spec if present (legacy wrapper), else top-level (v4 flat).
	source := fullChart
	if spec, ok := fullChart["spec"].(map[string]any); ok {
		source = spec
	}

	// Build filtered schema with only Ledger-compatible fields
	filtered := make(map[string]any)

	if chart, ok := source["chart"]; ok {
		filtered["chart"] = chart
	}
	if transactions, ok := source["transactions"]; ok {
		filtered["transactions"] = transactions
	}
	if queries, ok := source["queries"]; ok {
		filtered["queries"] = queries
	}

	// If no chart at all, that's an error
	if len(filtered) == 0 || filtered["chart"] == nil {
		return nil, errors.New("no chart field found in spec")
	}

	// Ensure transactions exists (can be empty map)
	if filtered["transactions"] == nil {
		filtered["transactions"] = map[string]any{}
	}

	if chart, ok := filtered["chart"].(map[string]any); ok {
		// Stringify non-string .metadata defaults (bool, number, object → string).
		// The Formance SDK (V2ChartAccountMetadata.Default) only accepts *string.
		stringifyMetadataDefaults(chart)

		// Strip v4-only fields from .metadata (keep only "default").
		stripMetadataExtras(chart)

		// Validate that no node has multiple $-prefixed variable segments
		// as siblings — the Ledger rejects this as ambiguous.
		if segErrs := validateChartSegments(chart, "chart"); len(segErrs) > 0 {
			return nil, fmt.Errorf("chart segment validation failed:\n  - %s", strings.Join(segErrs, "\n  - "))
		}
	}

	// Strip v4-only fields from transaction templates (keep only script, description, runtime).
	if txs, ok := filtered["transactions"].(map[string]any); ok {
		stripTransactionExtras(txs)
	}

	result, err := json.Marshal(filtered)
	if err != nil {
		return nil, fmt.Errorf("marshaling filtered schema: %w", err)
	}
	return result, nil
}

// ExtractLedgerName extracts the ledger name from the chart.
// The ledger name is required and comes from ledger.name in the chart YAML.
// Returns an error if the ledger name is not found.
//
// Supports both v4 flat format (ledger at top level) and legacy
// Kubernetes wrapper format (ledger inside spec).
func (r *Result) ExtractLedgerName() (string, error) {
	if !r.Valid || len(r.Errors) > 0 {
		return "", errors.New("cannot extract from invalid chart")
	}

	var fullChart map[string]any
	if err := json.Unmarshal(r.JSON, &fullChart); err != nil {
		return "", fmt.Errorf("parsing chart JSON: %w", err)
	}

	// Determine source: use spec if present (legacy wrapper), else top-level (v4 flat).
	source := fullChart
	if spec, ok := fullChart["spec"].(map[string]any); ok {
		source = spec
	}

	// Extract ledger config
	ledger, ok := source["ledger"].(map[string]any)
	if !ok {
		return "", errors.New("ledger configuration not found in spec — ledger.name is required")
	}

	// Extract ledger name
	name, ok := ledger["name"].(string)
	if !ok || name == "" {
		return "", errors.New("ledger.name not found or empty in chart spec")
	}

	return name, nil
}

// ExtractVersion extracts the version string from the chart.
// Supports both v4 flat format (version at top level) and legacy
// Kubernetes wrapper format (version inside spec).
func (r *Result) ExtractVersion() (string, error) {
	if !r.Valid || len(r.Errors) > 0 {
		return "", errors.New("cannot extract from invalid chart")
	}

	var fullChart map[string]any
	if err := json.Unmarshal(r.JSON, &fullChart); err != nil {
		return "", fmt.Errorf("parsing chart JSON: %w", err)
	}

	// Determine source: use spec if present (legacy wrapper), else top-level (v4 flat).
	source := fullChart
	if spec, ok := fullChart["spec"].(map[string]any); ok {
		source = spec
	}

	// Extract version — accept string or number.
	switch v := source["version"].(type) {
	case string:
		if v == "" {
			return "", errors.New("version field is empty")
		}
		return v, nil
	case float64:
		return fmt.Sprintf("%g", v), nil
	default:
		return "", errors.New("version field not found or invalid in chart")
	}
}

// Validate reads chartPath, converts YAML to JSON (resolving all YAML features),
// and validates the result against the compiled schema.
func Validate(chartPath string) (*Result, error) {
	if compiledSchema == nil {
		return nil, errors.New("schema not loaded — call LoadSchema first")
	}

	raw, err := os.ReadFile(chartPath)
	if err != nil {
		return nil, fmt.Errorf("reading chart: %w", err)
	}

	jsonBytes, err := convert.ToJSON(raw)
	if err != nil {
		return &Result{
			Valid:  false,
			Errors: []string{err.Error()},
		}, nil
	}

	result, err := compiledSchema.Validate(gojsonschema.NewBytesLoader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("running schema validator: %w", err)
	}

	var errs []string
	for _, desc := range result.Errors() {
		errs = append(errs, desc.String())
	}

	return &Result{
		Valid:  result.Valid(),
		Errors: errs,
		JSON:   jsonBytes,
	}, nil
}

// validateChartSegments walks a chart segment tree and detects patterns that the
// Formance Ledger would reject. Specifically, it checks that no object has more
// than one $-prefixed (variable) child key at the same level. Two variable
// segments as siblings are ambiguous — the Ledger cannot distinguish which
// variable an address component should bind to.
//
// Returns a list of human-readable error strings, or nil if valid.
func validateChartSegments(node map[string]any, path string) []string {
	var errs []string

	// Collect $-prefixed keys at this level.
	var varKeys []string
	for k := range node {
		if strings.HasPrefix(k, "$") {
			varKeys = append(varKeys, k)
		}
	}

	if len(varKeys) > 1 {
		slices.Sort(varKeys)
		errs = append(errs, fmt.Sprintf(
			"invalid segment %q: cannot have two variable segments (%s) at the same level",
			path, strings.Join(varKeys, ", "),
		))
	}

	// Recurse into child segments (skip dot-prefixed keys like .metadata, .self).
	for k, v := range node {
		if strings.HasPrefix(k, ".") {
			continue
		}
		child, ok := v.(map[string]any)
		if !ok {
			continue
		}
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		errs = append(errs, validateChartSegments(child, childPath)...)
	}

	return errs
}

// ledgerTransactionFields are the only fields the Ledger API accepts on
// V2TransactionTemplate. Everything else is a v4 chart extension.
var ledgerTransactionFields = map[string]bool{
	"script":      true,
	"description": true,
	"runtime":     true,
}

// stripTransactionExtras removes v4-only fields from transaction templates.
// The Ledger API (V2TransactionTemplate) only accepts script, description,
// and runtime. Fields like bus_trigger, timing, category, priority, depends_on,
// etc. are v4 chart extensions that the Ledger silently ignores.
func stripTransactionExtras(txs map[string]any) {
	for name, v := range txs {
		tx, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for k := range tx {
			if !ledgerTransactionFields[k] {
				delete(tx, k)
			}
		}
		txs[name] = tx
	}
}

// ledgerMetadataFields are the only fields the Ledger API accepts on
// V2ChartAccountMetadata. The v4 chart allows type, pattern, enum, etc.
var ledgerMetadataFields = map[string]bool{
	"default": true,
}

// stripMetadataExtras removes v4-only fields from .metadata entries.
// The Ledger API (V2ChartAccountMetadata) only accepts "default".
// Fields like type, pattern, enum, description, required, format are
// v4 chart extensions for documentation and validation hints.
func stripMetadataExtras(node map[string]any) {
	if meta, ok := node[".metadata"].(map[string]any); ok {
		for _, field := range meta {
			fieldMap, ok := field.(map[string]any)
			if !ok {
				continue
			}
			for k := range fieldMap {
				if !ledgerMetadataFields[k] {
					delete(fieldMap, k)
				}
			}
		}
	}

	// Recurse into child segments.
	for k, v := range node {
		if strings.HasPrefix(k, ".") {
			continue
		}
		if child, ok := v.(map[string]any); ok {
			stripMetadataExtras(child)
		}
	}
}

// stringifyMetadataDefaults walks a chart segment tree and converts every
// .metadata default value to a JSON string. The Formance Ledger SDK
// (V2ChartAccountMetadata.Default) only accepts *string, but chart YAML
// files use booleans, numbers, and objects for readability.
//
// Conversion rules:
//   - string  → kept as-is
//   - bool    → "true" / "false"
//   - number  → decimal string via fmt
//   - object  → compact JSON string
//   - null    → removed (omitempty in SDK)
func stringifyMetadataDefaults(node map[string]any) {
	// Process .metadata at this level.
	if meta, ok := node[".metadata"].(map[string]any); ok {
		for key, field := range meta {
			fieldMap, ok := field.(map[string]any)
			if !ok {
				continue
			}
			val, exists := fieldMap["default"]
			if !exists {
				continue
			}
			switch v := val.(type) {
			case string:
				// Already a string — nothing to do.
			case nil:
				delete(fieldMap, "default")
			default:
				// Bool, number, or object → JSON-encode to string.
				b, err := json.Marshal(v)
				if err == nil {
					fieldMap["default"] = string(b)
				}
			}
			meta[key] = fieldMap
		}
	}

	// Recurse into child segments (any key not starting with ".").
	for k, v := range node {
		if strings.HasPrefix(k, ".") {
			continue
		}
		if child, ok := v.(map[string]any); ok {
			stringifyMetadataDefaults(child)
		}
	}
}
