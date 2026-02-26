package chart

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata")
}

func schemaDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "schema", "chart_v4.schema.json")
}

func ledgerSchemaDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "schema", "ledger_v2_schema_data.schema.json")
}

func TestValidate_ValidChart(t *testing.T) {
	if err := LoadSchema(schemaDir()); err != nil {
		t.Fatalf("loading schema: %v", err)
	}

	path := filepath.Join(testdataDir(), "valid.chart.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", path)
	}

	result, err := Validate(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid chart, got errors:\n  - %s", result.ErrorSummary())
	}
	if len(result.JSON) == 0 {
		t.Error("expected non-empty JSON output")
	}
}

func TestValidate_AnchorsChart(t *testing.T) {
	if err := LoadSchema(schemaDir()); err != nil {
		t.Fatalf("loading schema: %v", err)
	}

	path := filepath.Join(testdataDir(), "anchors.chart.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", path)
	}

	result, err := Validate(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Valid {
		t.Log("Note: anchors.chart.yaml has extra top-level 'defaults' key — may fail strict schema")
	}
}

func TestValidate_InvalidChart(t *testing.T) {
	if err := LoadSchema(schemaDir()); err != nil {
		t.Fatalf("loading schema: %v", err)
	}

	path := filepath.Join(testdataDir(), "invalid.chart.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", path)
	}

	result, err := Validate(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid chart to fail validation")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one validation error")
	}
}

func TestValidate_SchemaNotLoaded(t *testing.T) {
	old := compiledSchema
	compiledSchema = nil
	defer func() { compiledSchema = old }()

	_, err := Validate("any-path.yaml")
	if err == nil {
		t.Error("expected error when schema not loaded")
	}
}

// TestExtractLedgerSchema_LegacyWrapper tests extraction from legacy Kubernetes wrapper format.
func TestExtractLedgerSchema_LegacyWrapper(t *testing.T) {
	fullChartJSON := []byte(`{
		"apiVersion": "formance.com/v1alpha1",
		"kind": "Chart",
		"metadata": {
			"name": "test-chart",
			"description": "Test chart"
		},
		"spec": {
			"chart": {
				"users": {
					"pattern": "users:*"
				}
			},
			"transactions": {
				"transfer": {
					"script": "send [GEN] to @user"
				}
			},
			"business": {
				"domain": "payments",
				"context": "This is business context"
			},
			"assets": [
				{
					"code": "USD",
					"precision": 2
				}
			]
		}
	}`)

	result := &Result{Valid: true, JSON: fullChartJSON}

	ledgerJSON, err := result.ExtractLedgerSchema()
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	var extracted map[string]any
	if err := json.Unmarshal(ledgerJSON, &extracted); err != nil {
		t.Fatalf("unmarshaling extracted JSON: %v", err)
	}

	if _, ok := extracted["chart"]; !ok {
		t.Error("extracted schema missing 'chart' field")
	}
	if _, ok := extracted["transactions"]; !ok {
		t.Error("extracted schema missing 'transactions' field")
	}

	forbidden := []string{"business", "assets", "ledger", "placeholders", "reporting", "apiVersion", "kind", "metadata", "spec"}
	for _, field := range forbidden {
		if _, exists := extracted[field]; exists {
			t.Errorf("extracted schema should not contain field: %q", field)
		}
	}

	chartData := extracted["chart"].(map[string]any)
	if _, ok := chartData["users"]; !ok {
		t.Error("chart should contain 'users' account")
	}

	txData := extracted["transactions"].(map[string]any)
	if _, ok := txData["transfer"]; !ok {
		t.Error("transactions should contain 'transfer' template")
	}
}

// TestExtractLedgerSchema_V4Flat tests extraction from v4 flat format (no spec wrapper).
func TestExtractLedgerSchema_V4Flat(t *testing.T) {
	v4JSON := []byte(`{
		"version": "4",
		"createdAt": "2025-09-16T00:00:00Z",
		"chart": {
			"merchants": {
				".self": {}
			}
		},
		"transactions": {
			"PAYMENT": {
				"script": "send [USD/2 100] (\n  source = @world\n  destination = @merchants:123\n)"
			}
		},
		"business": {
			"domain": "payments"
		},
		"ledger": {
			"name": "test-ledger"
		},
		"assets": [
			{"code": "USD", "precision": 2}
		]
	}`)

	result := &Result{Valid: true, JSON: v4JSON}

	ledgerJSON, err := result.ExtractLedgerSchema()
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	var extracted map[string]any
	if err := json.Unmarshal(ledgerJSON, &extracted); err != nil {
		t.Fatalf("unmarshaling extracted JSON: %v", err)
	}

	if _, ok := extracted["chart"]; !ok {
		t.Error("extracted schema missing 'chart' field")
	}
	if _, ok := extracted["transactions"]; !ok {
		t.Error("extracted schema missing 'transactions' field")
	}

	stripped := []string{"business", "assets", "ledger", "version", "createdAt", "placeholders", "reportings", "settings"}
	for _, field := range stripped {
		if _, exists := extracted[field]; exists {
			t.Errorf("extracted schema should not contain field: %q", field)
		}
	}
}

func TestExtractLedgerName(t *testing.T) {
	tests := []struct {
		name      string
		chartJSON []byte
		wantName  string
		wantErr   bool
	}{
		{
			name: "Valid ledger name (legacy wrapper)",
			chartJSON: []byte(`{
				"apiVersion": "formance.com/v1alpha1",
				"kind": "Chart",
				"spec": {
					"ledger": {"name": "payments-ledger"},
					"chart": {}
				}
			}`),
			wantName: "payments-ledger",
		},
		{
			name: "Valid ledger name (v4 flat)",
			chartJSON: []byte(`{
				"version": "4",
				"createdAt": "2025-09-16T00:00:00Z",
				"ledger": {"name": "my-v4-ledger"},
				"chart": {}
			}`),
			wantName: "my-v4-ledger",
		},
		{
			name: "Missing ledger config (legacy)",
			chartJSON: []byte(`{
				"apiVersion": "formance.com/v1alpha1",
				"kind": "Chart",
				"spec": {"chart": {}}
			}`),
			wantErr: true,
		},
		{
			name: "Missing ledger config (v4 flat)",
			chartJSON: []byte(`{
				"version": "4",
				"createdAt": "2025-09-16T00:00:00Z",
				"chart": {}
			}`),
			wantErr: true,
		},
		{
			name:      "Missing ledger name",
			chartJSON: []byte(`{"ledger": {}, "chart": {}}`),
			wantErr:   true,
		},
		{
			name:      "Empty ledger name",
			chartJSON: []byte(`{"ledger": {"name": ""}, "chart": {}}`),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{Valid: true, JSON: tt.chartJSON}

			got, err := result.ExtractLedgerName()
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractLedgerName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.wantName {
				t.Errorf("ExtractLedgerName() = %q, want %q", got, tt.wantName)
			}
		})
	}

	invalidResult := &Result{Valid: false, Errors: []string{"validation failed"}}
	_, err := invalidResult.ExtractLedgerName()
	if err == nil {
		t.Error("ExtractLedgerName() should fail on invalid chart")
	}
}

// TestStringifyMetadataDefaults verifies that non-string .metadata defaults
// (booleans, numbers, objects) are converted to JSON strings, as required by
// the Formance SDK (V2ChartAccountMetadata.Default is *string).
func TestStringifyMetadataDefaults(t *testing.T) {
	chartJSON := []byte(`{
		"chart": {
			"bank": {
				".self": {},
				".metadata": {
					"nature":        {"default": "asset"},
					"safeguarding":  {"default": true},
					"base_precision":{"default": 2},
					"reconciliation":{"default": {"role": "client_bank", "match_by": ["amount"]}},
					"empty_default": {"default": null}
				},
				"sub": {
					".metadata": {
						"active": {"default": false}
					}
				}
			}
		},
		"transactions": {}
	}`)

	result := &Result{Valid: true, JSON: chartJSON}
	ledgerJSON, err := result.ExtractLedgerSchema()
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	var extracted map[string]any
	if err := json.Unmarshal(ledgerJSON, &extracted); err != nil {
		t.Fatalf("unmarshaling extracted JSON: %v", err)
	}

	bank := extracted["chart"].(map[string]any)["bank"].(map[string]any)
	meta := bank[".metadata"].(map[string]any)

	// String stays as string.
	natureDefault := meta["nature"].(map[string]any)["default"]
	if s, ok := natureDefault.(string); !ok || s != "asset" {
		t.Errorf("nature default: want string %q, got %T %v", "asset", natureDefault, natureDefault)
	}

	// Boolean → "true".
	safeguardDefault := meta["safeguarding"].(map[string]any)["default"]
	if s, ok := safeguardDefault.(string); !ok || s != "true" {
		t.Errorf("safeguarding default: want string %q, got %T %v", "true", safeguardDefault, safeguardDefault)
	}

	// Number → "2".
	precisionDefault := meta["base_precision"].(map[string]any)["default"]
	if s, ok := precisionDefault.(string); !ok || s != "2" {
		t.Errorf("base_precision default: want string %q, got %T %v", "2", precisionDefault, precisionDefault)
	}

	// Object → JSON string.
	reconDefault := meta["reconciliation"].(map[string]any)["default"]
	reconStr, ok := reconDefault.(string)
	if !ok {
		t.Fatalf("reconciliation default: want string, got %T %v", reconDefault, reconDefault)
	}
	var reconParsed map[string]any
	if err := json.Unmarshal([]byte(reconStr), &reconParsed); err != nil {
		t.Errorf("reconciliation default is not valid JSON: %v", err)
	}
	if reconParsed["role"] != "client_bank" {
		t.Errorf("reconciliation.role: want %q, got %v", "client_bank", reconParsed["role"])
	}

	// Null → removed.
	emptyField := meta["empty_default"].(map[string]any)
	if _, exists := emptyField["default"]; exists {
		t.Error("null default should be removed")
	}

	// Nested: boolean → "false".
	subMeta := bank["sub"].(map[string]any)[".metadata"].(map[string]any)
	activeDefault := subMeta["active"].(map[string]any)["default"]
	if s, ok := activeDefault.(string); !ok || s != "false" {
		t.Errorf("nested active default: want string %q, got %T %v", "false", activeDefault, activeDefault)
	}
}

// TestExtractLedgerSchema_SDKCompatible verifies that ExtractLedgerSchema
// output can be unmarshalled into the Formance SDK V2SchemaData type.
func TestExtractLedgerSchema_SDKCompatible(t *testing.T) {
	// Simulate chart with mixed-type metadata defaults.
	chartJSON := []byte(`{
		"chart": {
			"accounts": {
				".self": {},
				".metadata": {
					"nature":  {"default": "asset"},
					"active":  {"default": true},
					"config":  {"default": {"key": "value"}}
				}
			}
		},
		"transactions": {
			"DEPOSIT": {"script": "send [USD/2 100] (source = @world destination = @accounts:123)"}
		}
	}`)

	result := &Result{Valid: true, JSON: chartJSON}
	ledgerJSON, err := result.ExtractLedgerSchema()
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	// Simulate what push.Push() does: unmarshal into V2SchemaData-like structure.
	// V2ChartAccountMetadata has Default *string, so all defaults must be strings.
	type metadataField struct {
		Default *string `json:"default,omitempty"`
	}
	type segment struct {
		Metadata map[string]metadataField `json:".metadata,omitempty"`
	}
	type schemaData struct {
		Chart        map[string]segment `json:"chart"`
		Transactions map[string]any     `json:"transactions"`
	}

	var sd schemaData
	if err := json.Unmarshal(ledgerJSON, &sd); err != nil {
		t.Fatalf("unmarshal into SDK-like types failed (this is the bug we're fixing): %v", err)
	}

	meta := sd.Chart["accounts"].Metadata
	if meta["nature"].Default == nil || *meta["nature"].Default != "asset" {
		t.Error("nature default should be 'asset'")
	}
	if meta["active"].Default == nil || *meta["active"].Default != "true" {
		t.Errorf("active default should be 'true', got %v", meta["active"].Default)
	}
	if meta["config"].Default == nil {
		t.Fatal("config default should not be nil")
	}
	// Verify the JSON-stringified object is parseable.
	var obj map[string]any
	if err := json.Unmarshal([]byte(*meta["config"].Default), &obj); err != nil {
		t.Errorf("config default is not valid JSON: %v", err)
	}
}

// TestValidateChartSegments verifies that duplicate variable segments ($-prefixed
// keys) at the same level are detected and rejected.
func TestValidateChartSegments(t *testing.T) {
	tests := []struct {
		name      string
		chartJSON string
		wantErr   bool
		errContains string
	}{
		{
			name: "valid — single variable segment",
			chartJSON: `{
				"chart": {
					"platforms": {
						"$platform": {
							"main": null,
							"revenue": null
						}
					}
				},
				"transactions": {}
			}`,
		},
		{
			name: "valid — variable segments at different levels",
			chartJSON: `{
				"chart": {
					"platforms": {
						"$platform": {
							"$acquirer": {
								"costs": null
							}
						}
					}
				},
				"transactions": {}
			}`,
		},
		{
			name: "invalid — two variable siblings",
			chartJSON: `{
				"chart": {
					"platforms": {
						"$platform": {
							"main": null,
							"$acquirer": {"costs": null},
							"$scheme":   {"costs": null}
						}
					}
				},
				"transactions": {}
			}`,
			wantErr:     true,
			errContains: "$acquirer",
		},
		{
			name: "invalid — three variable siblings",
			chartJSON: `{
				"chart": {
					"root": {
						"$a": null,
						"$b": null,
						"$c": null
					}
				},
				"transactions": {}
			}`,
			wantErr:     true,
			errContains: "two variable segments",
		},
		{
			name: "valid — no variable segments at all",
			chartJSON: `{
				"chart": {
					"users": {
						"main": null,
						"savings": null
					}
				},
				"transactions": {}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &Result{Valid: true, JSON: []byte(tt.chartJSON)}
			_, err := result.ExtractLedgerSchema()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestValidateAllTestdata validates every YAML file under testdata/ against
// the chart_v4 schema. This is the gate that must pass before any chart is
// sent to the Formance stack via InsertSchema.
func TestValidateAllTestdata(t *testing.T) {
	if err := LoadSchema(schemaDir()); err != nil {
		t.Fatalf("loading chart_v4 schema: %v", err)
	}

	var files []string
	err := filepath.Walk(testdataDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking testdata: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no YAML files found in testdata/")
	}

	t.Logf("Found %d testdata chart file(s)", len(files))

	for _, f := range files {
		rel, _ := filepath.Rel(testdataDir(), f)
		t.Run(rel, func(t *testing.T) {
			result, err := Validate(f)
			if err != nil {
				t.Fatalf("validate error: %v", err)
			}
			if !result.Valid {
				t.Errorf("chart_v4 schema validation failed:\n  - %s", result.ErrorSummary())
			}
		})
	}
}

// TestExtractAllTestdata verifies that every valid testdata chart produces
// a valid Ledger-compatible schema via ExtractLedgerSchema. This ensures the
// extracted payload is safe to send to Formance InsertSchema.
func TestExtractAllTestdata(t *testing.T) {
	if err := LoadSchema(schemaDir()); err != nil {
		t.Fatalf("loading chart_v4 schema: %v", err)
	}

	var files []string
	err := filepath.Walk(testdataDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking testdata: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no YAML files found in testdata/")
	}

	for _, f := range files {
		rel, _ := filepath.Rel(testdataDir(), f)
		t.Run(rel, func(t *testing.T) {
			result, err := Validate(f)
			if err != nil {
				t.Fatalf("validate error: %v", err)
			}
			if !result.Valid {
				t.Skipf("skipping extraction for invalid chart: %s", result.ErrorSummary())
			}

			// 1. Extract ledger name
			ledgerName, err := result.ExtractLedgerName()
			if err != nil {
				t.Fatalf("ExtractLedgerName() failed: %v", err)
			}
			if ledgerName == "" {
				t.Error("extracted ledger name is empty")
			}
			t.Logf("ledger name: %s", ledgerName)

			// 2. Extract ledger schema (may fail for charts with duplicate variable segments)
			ledgerJSON, err := result.ExtractLedgerSchema()
			if err != nil {
				if strings.Contains(err.Error(), "chart segment validation failed") {
					t.Skipf("chart has segment violations (would be rejected by Ledger): %v", err)
				}
				t.Fatalf("ExtractLedgerSchema() failed: %v", err)
			}

			// 3. Verify extracted JSON is valid
			var extracted map[string]any
			if err := json.Unmarshal(ledgerJSON, &extracted); err != nil {
				t.Fatalf("extracted JSON is not valid: %v", err)
			}

			// 4. Must have chart field with content
			chartData, ok := extracted["chart"]
			if !ok {
				t.Fatal("extracted schema missing 'chart' field")
			}
			chartMap, ok := chartData.(map[string]any)
			if !ok {
				t.Fatal("chart field is not an object")
			}
			if len(chartMap) == 0 {
				t.Error("chart field is empty — no accounts defined")
			}

			// 5. Must have transactions field (even if empty)
			if _, ok := extracted["transactions"]; !ok {
				t.Error("extracted schema missing 'transactions' field")
			}

			// 6. Must NOT contain fields that don't belong in Ledger API payload
			forbidden := []string{
				"business", "assets", "ledger", "version", "createdAt",
				"date", "placeholders", "reportings", "settings",
				"apiVersion", "kind", "metadata", "spec",
			}
			for _, field := range forbidden {
				if _, exists := extracted[field]; exists {
					t.Errorf("extracted schema must not contain %q — would be rejected by InsertSchema", field)
				}
			}

			t.Logf("extracted schema OK: %d top-level fields, chart has %d account segments",
				len(extracted), len(chartMap))
		})
	}
}

// TestValidateLedgerPayload_Valid verifies that well-formed Ledger payloads
// pass schema validation.
func TestValidateLedgerPayload_Valid(t *testing.T) {
	if err := LoadLedgerSchema(ledgerSchemaDir()); err != nil {
		t.Fatalf("loading ledger schema: %v", err)
	}

	tests := []struct {
		name string
		json string
	}{
		{
			name: "minimal — chart and transactions only",
			json: `{"chart":{"users":null},"transactions":{}}`,
		},
		{
			name: "with transaction template",
			json: `{
				"chart":{"users":{"$userid":{"main":null}}},
				"transactions":{
					"DEPOSIT":{"script":"send [USD/2 100] (source = @world destination = @users:123:main)"}
				}
			}`,
		},
		{
			name: "with runtime on transaction",
			json: `{
				"chart":{"users":null},
				"transactions":{
					"TX":{"script":"send ...","runtime":"experimental-interpreter"}
				}
			}`,
		},
		{
			name: "with description on transaction",
			json: `{
				"chart":{"users":null},
				"transactions":{
					"TX":{"script":"send ...","description":"A deposit"}
				}
			}`,
		},
		{
			name: "with queries",
			json: `{
				"chart":{"accounts":null},
				"transactions":{},
				"queries":{
					"balance":{"resource":"accounts","body":{"match":{"metadata[userid]":"123"}}}
				}
			}`,
		},
		{
			name: "with metadata default (string)",
			json: `{
				"chart":{
					"bank":{
						".metadata":{"nature":{"default":"asset"}}
					}
				},
				"transactions":{}
			}`,
		},
		{
			name: "with .self and .pattern",
			json: `{
				"chart":{
					"merchants":{
						".self":{},
						".pattern":"[a-z]+",
						"sub":null
					}
				},
				"transactions":{}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateLedgerPayload([]byte(tt.json)); err != nil {
				t.Errorf("expected valid payload, got error: %v", err)
			}
		})
	}
}

// TestValidateLedgerPayload_Invalid verifies that payloads violating the
// Ledger API schema are rejected with clear errors.
func TestValidateLedgerPayload_Invalid(t *testing.T) {
	if err := LoadLedgerSchema(ledgerSchemaDir()); err != nil {
		t.Fatalf("loading ledger schema: %v", err)
	}

	tests := []struct {
		name        string
		json        string
		errContains string
	}{
		{
			name:        "missing transactions",
			json:        `{"chart":{"users":null}}`,
			errContains: "transactions",
		},
		{
			name:        "missing chart",
			json:        `{"transactions":{}}`,
			errContains: "chart",
		},
		{
			name:        "extra top-level field",
			json:        `{"chart":{"users":null},"transactions":{},"business":{"domain":"test"}}`,
			errContains: "business",
		},
		{
			name:        "extra field on transaction template",
			json:        `{"chart":{},"transactions":{"TX":{"script":"send ...","bus_trigger":"payment.created"}}}`,
			errContains: "bus_trigger",
		},
		{
			name:        "transaction missing script",
			json:        `{"chart":{},"transactions":{"TX":{"description":"no script"}}}`,
			errContains: "script",
		},
		{
			name:        "invalid runtime value",
			json:        `{"chart":{},"transactions":{"TX":{"script":"send ...","runtime":"invalid-runtime"}}}`,
			errContains: "runtime",
		},
		{
			name:        "metadata with extra fields (type, pattern)",
			json:        `{"chart":{"bank":{".metadata":{"nature":{"default":"asset","type":"string","pattern":"[a-z]+"}}}},"transactions":{}}`,
			errContains: "type",
		},
		{
			name:        "metadata default is not a string",
			json:        `{"chart":{"bank":{".metadata":{"active":{"default":true}}}},"transactions":{}}`,
			errContains: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLedgerPayload([]byte(tt.json))
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
			}
		})
	}
}

// TestValidateLedgerPayload_SchemaNotLoaded verifies error when schema not loaded.
func TestValidateLedgerPayload_SchemaNotLoaded(t *testing.T) {
	old := compiledLedgerSchema
	compiledLedgerSchema = nil
	defer func() { compiledLedgerSchema = old }()

	err := ValidateLedgerPayload([]byte(`{"chart":{},"transactions":{}}`))
	if err == nil {
		t.Error("expected error when ledger schema not loaded")
	}
}

// TestStripTransactionExtras verifies that v4-only fields are removed from
// transaction templates, keeping only script, description, and runtime.
func TestStripTransactionExtras(t *testing.T) {
	chartJSON := []byte(`{
		"chart": {"accounts": null},
		"transactions": {
			"PAYMENT": {
				"script": "send ...",
				"description": "A payment",
				"runtime": "experimental-interpreter",
				"bus_trigger": "payment.created",
				"timing": "real-time",
				"category": "payments",
				"priority": 1,
				"depends_on": ["DEPOSIT"],
				"preconditions": {"balance_check": true}
			}
		}
	}`)

	result := &Result{Valid: true, JSON: chartJSON}
	ledgerJSON, err := result.ExtractLedgerSchema()
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	var extracted map[string]any
	if err := json.Unmarshal(ledgerJSON, &extracted); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}

	tx := extracted["transactions"].(map[string]any)["PAYMENT"].(map[string]any)

	// These should be kept.
	for _, keep := range []string{"script", "description", "runtime"} {
		if _, ok := tx[keep]; !ok {
			t.Errorf("expected field %q to be kept", keep)
		}
	}

	// These should be stripped.
	for _, strip := range []string{"bus_trigger", "timing", "category", "priority", "depends_on", "preconditions"} {
		if _, ok := tx[strip]; ok {
			t.Errorf("expected field %q to be stripped", strip)
		}
	}
}

// TestStripMetadataExtras verifies that v4-only metadata fields are removed,
// keeping only "default".
func TestStripMetadataExtras(t *testing.T) {
	chartJSON := []byte(`{
		"chart": {
			"bank": {
				".metadata": {
					"nature": {
						"default": "asset",
						"type": "string",
						"pattern": "[a-z]+",
						"enum": ["asset", "liability"],
						"description": "Account nature",
						"required": true,
						"format": "enum"
					}
				},
				"sub": {
					".metadata": {
						"active": {
							"default": "true",
							"type": "boolean",
							"description": "Is active"
						}
					}
				}
			}
		},
		"transactions": {}
	}`)

	result := &Result{Valid: true, JSON: chartJSON}
	ledgerJSON, err := result.ExtractLedgerSchema()
	if err != nil {
		t.Fatalf("extract failed: %v", err)
	}

	var extracted map[string]any
	if err := json.Unmarshal(ledgerJSON, &extracted); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}

	bank := extracted["chart"].(map[string]any)["bank"].(map[string]any)
	meta := bank[".metadata"].(map[string]any)

	// Nature: only "default" should remain.
	nature := meta["nature"].(map[string]any)
	if _, ok := nature["default"]; !ok {
		t.Error("nature should keep 'default'")
	}
	for _, stripped := range []string{"type", "pattern", "enum", "description", "required", "format"} {
		if _, ok := nature[stripped]; ok {
			t.Errorf("nature should not have %q after stripping", stripped)
		}
	}

	// Nested sub metadata: only "default" should remain.
	subMeta := bank["sub"].(map[string]any)[".metadata"].(map[string]any)
	active := subMeta["active"].(map[string]any)
	if _, ok := active["default"]; !ok {
		t.Error("active should keep 'default'")
	}
	for _, stripped := range []string{"type", "description"} {
		if _, ok := active[stripped]; ok {
			t.Errorf("active should not have %q after stripping", stripped)
		}
	}
}

// TestValidateLedgerPayload_AllTestdata validates that every testdata chart,
// after extraction, passes the Ledger API JSON Schema validation. This is the
// end-to-end gate: YAML → v4 validate → extract → Ledger schema validate.
func TestValidateLedgerPayload_AllTestdata(t *testing.T) {
	if err := LoadSchema(schemaDir()); err != nil {
		t.Fatalf("loading chart_v4 schema: %v", err)
	}
	if err := LoadLedgerSchema(ledgerSchemaDir()); err != nil {
		t.Fatalf("loading ledger schema: %v", err)
	}

	var files []string
	err := filepath.Walk(testdataDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yaml" || filepath.Ext(path) == ".yml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking testdata: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no YAML files found in testdata/")
	}

	for _, f := range files {
		rel, _ := filepath.Rel(testdataDir(), f)
		t.Run(rel, func(t *testing.T) {
			result, err := Validate(f)
			if err != nil {
				t.Fatalf("validate error: %v", err)
			}
			if !result.Valid {
				t.Skipf("skipping — chart_v4 invalid: %s", result.ErrorSummary())
			}

			ledgerJSON, err := result.ExtractLedgerSchema()
			if err != nil {
				if strings.Contains(err.Error(), "chart segment validation failed") {
					t.Skipf("chart has segment violations: %v", err)
				}
				t.Fatalf("ExtractLedgerSchema() failed: %v", err)
			}

			if err := ValidateLedgerPayload(ledgerJSON); err != nil {
				// Log the payload for debugging.
				t.Logf("Extracted Ledger payload:\n%s", string(ledgerJSON))
				t.Errorf("Ledger schema validation failed:\n%v", err)
			}
		})
	}
}
