package push

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/formancehq/ledger/pkg/client"
	"github.com/formancehq/ledger/pkg/client/models/components"
	"github.com/formancehq/ledger/pkg/client/models/operations"
	"github.com/formancehq/ledger/pkg/client/models/sdkerrors"
)

// Provenance captures the origin of a chart file for traceability.
type Provenance struct {
	Repository string // e.g. "org/repo"
	Branch     string // e.g. "main"
	FilePath   string // relative path within the repo
	CommitSHA  string // full 40-char git revision
}

// Client wraps the Formance Ledger SDK for pushing chart data.
type Client struct {
	serverURL   string
	ledger      string
	baseVersion string
	sdk         *client.Formance
}

// New creates a push client. The SDK handles OAuth2 client_credentials
// internally, so no separate authentication step is needed.
//
// On Formance Cloud the Ledger API lives under /api/ledger (the gateway
// routes each service by prefix). New auto-detects this by probing
// {serverURL}/api/ledger/_/info. If it responds, the SDK is pointed at
// {serverURL}/api/ledger; otherwise it uses serverURL directly (self-hosted).
func New(serverURL, clientID, clientSecret, ledger, baseVersion string) *Client {
	serverURL = strings.TrimRight(serverURL, "/")

	// Detect Formance Cloud gateway.
	sdkBase := detectLedgerBase(serverURL)

	tokenURL := serverURL + "/api/auth/oauth/token"
	sdk := client.New(
		client.WithServerURL(sdkBase),
		client.WithSecurity(components.Security{
			ClientID:     &clientID,
			ClientSecret: &clientSecret,
			TokenURL:     &tokenURL,
		}),
	)

	return &Client{
		serverURL:   serverURL,
		ledger:      ledger,
		baseVersion: baseVersion,
		sdk:         sdk,
	}
}

// detectLedgerBase probes {serverURL}/api/ledger/_/info to determine whether
// the server is behind the Formance Cloud gateway. Returns the SDK base URL.
func detectLedgerBase(serverURL string) string {
	// If the URL already includes the /api/ledger path, use it directly.
	if strings.HasSuffix(serverURL, "/api/ledger") {
		return serverURL
	}

	candidate := serverURL + "/api/ledger"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Get(candidate + "/_/info")
	if err != nil {
		return serverURL
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK && strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return candidate
	}
	return serverURL
}

// Ledger returns the configured ledger name.
func (c *Client) Ledger() string { return c.ledger }

// SetLedger overrides the configured ledger name.
func (c *Client) SetLedger(name string) { c.ledger = name }

// SetBaseVersion overrides the base version prefix.
func (c *Client) SetBaseVersion(v string) { c.baseVersion = v }

// SDKVersion returns the Formance SDK version string.
func (c *Client) SDKVersion() string { return c.sdk.SDKVersion }

// isEmptyResponse returns true when the SDK returns an error because the
// server responded with HTTP 200 but an empty body (no Content-Type header).
// The Formance Cloud API does this for resources that don't exist yet or
// have no data. The Speakeasy-generated SDK treats missing Content-Type as
// an error: "unknown content-type received: : Status 200".
func isEmptyResponse(err error) bool {
	var sdkErr *sdkerrors.SDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.StatusCode == 200 && sdkErr.Body == ""
	}
	return false
}

// isNotFound returns true when the SDK returns a not-found error.
// The SDK may return this as an HTTP 404 (SDKError), a V2ErrorResponse with
// errorCode NOT_FOUND or LEDGER_NOT_FOUND, or an ErrorResponse with NOT_FOUND.
func isNotFound(err error) bool {
	var sdkErr *sdkerrors.SDKError
	if errors.As(err, &sdkErr) {
		return sdkErr.StatusCode == 404
	}
	var v2Err *sdkerrors.V2ErrorResponse
	if errors.As(err, &v2Err) {
		return v2Err.ErrorCode == components.V2ErrorsEnumNotFound ||
			v2Err.ErrorCode == components.V2ErrorsEnumLedgerNotFound
	}
	var errResp *sdkerrors.ErrorResponse
	if errors.As(err, &errResp) {
		return errResp.ErrorCode == components.ErrorsEnumNotFound
	}
	return false
}

// ComponentVersion describes one Formance service from /versions.
type ComponentVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Health  bool   `json:"health"`
}

// VersionInfo holds server component versions and SDK version.
type VersionInfo struct {
	Region     string             `json:"region"`
	Env        string             `json:"env"`
	Components []ComponentVersion `json:"versions"`
	SDKVersion string             `json:"sdkVersion"`
}

// LedgerVersion returns the ledger component version, or "unknown".
func (vi *VersionInfo) LedgerVersion() string {
	for _, comp := range vi.Components {
		if comp.Name == "ledger" {
			return comp.Version
		}
	}
	return "unknown"
}

// MinSchemasVersion is the minimum Ledger version that supports the /schemas
// API (plural endpoint). Earlier v2.4.0 betas used /schema (singular) which
// doesn't match the current SDK.
const MinSchemasVersion = "v2.4.0-rc.2"

// SupportsSchemas reports whether the Ledger version is >= MinSchemasVersion.
// Returns (supported, version) where version is the raw ledger version string.
// If the version can't be determined, returns (false, "unknown").
func (vi *VersionInfo) SupportsSchemas() (bool, string) {
	ver := vi.LedgerVersion()
	if ver == "unknown" {
		return false, ver
	}
	ok := compareSemver(ver, MinSchemasVersion) >= 0
	return ok, ver
}

// compareSemver compares two semver strings (with optional v prefix and
// pre-release tags). Returns -1, 0, or 1. Pre-release versions sort before
// their release (v2.4.0-rc.2 < v2.4.0). Among pre-releases, comparison is
// lexicographic on the tag.
func compareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)

	// Compare major.minor.patch.
	for i := range 3 {
		if pa.parts[i] != pb.parts[i] {
			if pa.parts[i] < pb.parts[i] {
				return -1
			}
			return 1
		}
	}

	// Equal version numbers — compare pre-release.
	// No pre-release > any pre-release (v2.4.0 > v2.4.0-rc.2).
	if pa.pre == "" && pb.pre == "" {
		return 0
	}
	if pa.pre == "" {
		return 1
	}
	if pb.pre == "" {
		return -1
	}
	return strings.Compare(pa.pre, pb.pre)
}

type semver struct {
	parts [3]int // major, minor, patch
	pre   string // pre-release tag (e.g. "beta.1", "rc.2")
}

func parseSemver(s string) semver {
	s = strings.TrimPrefix(s, "v")

	var sv semver
	// Split off pre-release.
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		sv.pre = s[idx+1:]
		s = s[:idx]
	}
	// Split off build metadata.
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}

	for i, part := range strings.SplitN(s, ".", 3) {
		sv.parts[i], _ = strconv.Atoi(part)
	}
	return sv
}

// GetVersionInfo fetches component versions from the Formance /versions
// endpoint (no auth required) and returns them alongside the SDK version.
func (c *Client) GetVersionInfo(ctx context.Context) (*VersionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/versions", nil)
	if err != nil {
		return nil, fmt.Errorf("creating version request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching versions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("versions endpoint returned HTTP %d", resp.StatusCode)
	}

	var vi VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&vi); err != nil {
		return nil, fmt.Errorf("decoding versions: %w", err)
	}
	vi.SDKVersion = c.sdk.SDKVersion
	return &vi, nil
}

// LedgerInfo holds basic ledger metadata returned by CheckLedger.
type LedgerInfo struct {
	Name    string `json:"name"`
	Bucket  string `json:"bucket"`
	Exists  bool   `json:"exists"`
	Message string `json:"message,omitempty"`
}

// CheckLedger verifies that the configured ledger is reachable and returns
// its metadata. Returns Exists=false if the ledger does not exist yet.
func (c *Client) CheckLedger(ctx context.Context) (*LedgerInfo, error) {
	if c.ledger == "" {
		return nil, fmt.Errorf("no ledger configured")
	}

	resp, err := c.sdk.Ledger.V2.GetLedger(ctx, operations.V2GetLedgerRequest{
		Ledger: c.ledger,
	})
	if err != nil {
		if isEmptyResponse(err) || isNotFound(err) {
			return &LedgerInfo{
				Name:   c.ledger,
				Exists: false,
			}, nil
		}
		return nil, fmt.Errorf("checking ledger %q: %w", c.ledger, err)
	}

	ledgerResp := resp.GetV2GetLedgerResponse()
	if ledgerResp == nil {
		return &LedgerInfo{
			Name:   c.ledger,
			Exists: false,
		}, nil
	}

	return &LedgerInfo{
		Name:   ledgerResp.Data.Name,
		Bucket: ledgerResp.Data.Bucket,
		Exists: true,
	}, nil
}

// CreateLedger creates a new ledger with the configured name.
func (c *Client) CreateLedger(ctx context.Context) error {
	if c.ledger == "" {
		return fmt.Errorf("no ledger configured")
	}

	_, err := c.sdk.Ledger.V2.CreateLedger(ctx, operations.V2CreateLedgerRequest{
		Ledger:                c.ledger,
		V2CreateLedgerRequest: components.V2CreateLedgerRequest{},
	})
	return err
}

// FileHash computes an 8-character hex hash of the raw YAML bytes.
// It uses SHA-256 and returns the first 8 hex characters.
func FileHash(rawYAML []byte) string {
	h := fmt.Sprintf("%x", sha256.Sum256(rawYAML))
	return h[:8]
}

// FileHashFromVersion extracts the 8-character file hash from an existing
// version string. The version format is:
// {baseVersion}+{repo}.{branch}.{filepath}.{commitSHA7}.{fileHash8}
// Returns the hash and true if found, or ("", false) if the version string
// does not contain a valid file hash.
func FileHashFromVersion(version string) (string, bool) {
	_, meta, hasMeta := strings.Cut(version, "+")
	if !hasMeta || meta == "" {
		return "", false
	}
	parts := strings.Split(meta, ".")
	if len(parts) < 2 {
		return "", false
	}
	hash := parts[len(parts)-1]
	if len(hash) != 8 {
		return "", false
	}
	return hash, true
}

// BuildVersion constructs a version string that encodes provenance metadata.
// Format: {baseVersion}+{repo}.{branch}.{filepath}.{commitSHA7}.{sha256hex8}
//
// The "+" follows semver build-metadata convention. The commit SHA and file
// hash are truncated for readability while remaining unique enough for lookup.
func (c *Client) BuildVersion(rawYAML []byte, prov Provenance) string {
	fileHash := FileHash(rawYAML)

	commitShort := prov.CommitSHA
	if len(commitShort) > 7 {
		commitShort = commitShort[:7]
	}

	meta := fmt.Sprintf("%s.%s.%s.%s.%s",
		sanitize(prov.Repository),
		sanitize(prov.Branch),
		sanitize(prov.FilePath),
		commitShort,
		fileHash,
	)

	return c.baseVersion + "+" + meta
}

// sanitize replaces characters that are not URL-path-safe with dashes.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// Push sends the chart JSON to Formance using the given version string.
func (c *Client) Push(ctx context.Context, version string, chartJSON []byte) error {
	var schemaData components.V2SchemaData
	if err := json.Unmarshal(chartJSON, &schemaData); err != nil {
		return fmt.Errorf("unmarshal into SDK types: %w", err)
	}

	_, err := c.sdk.Ledger.V2.InsertSchema(ctx, operations.V2InsertSchemaRequest{
		Ledger:       c.ledger,
		Version:      version,
		V2SchemaData: schemaData,
	})
	return err
}

// Schema is a lightweight representation of a remote schema for display.
type Schema struct {
	Version      string         `json:"version"`
	CreatedAt    time.Time      `json:"createdAt"`
	Chart        map[string]any `json:"chart"`
	Transactions map[string]any `json:"transactions"`
	Queries      map[string]any `json:"queries,omitempty"`
}

// ListSchemas returns all schema versions for the configured ledger.
// It paginates through all results automatically.
func (c *Client) ListSchemas(ctx context.Context) ([]Schema, error) {
	var all []Schema
	var cursor *string

	for {
		resp, err := c.sdk.Ledger.V2.ListSchemas(ctx, operations.V2ListSchemasRequest{
			Ledger: c.ledger,
			Cursor: cursor,
		})
		if err != nil {
			if isEmptyResponse(err) || isNotFound(err) {
				return nil, nil // no schemas or endpoint not available
			}
			return nil, err
		}

		cursorResp := resp.GetV2SchemasCursorResponse()
		if cursorResp == nil {
			break
		}

		for _, s := range cursorResp.Cursor.Data {
			all = append(all, schemaFromSDK(s))
		}

		if !cursorResp.Cursor.HasMore || cursorResp.Cursor.Next == nil {
			break
		}
		cursor = cursorResp.Cursor.Next
	}

	return all, nil
}

// GetSchema returns a single schema by version.
func (c *Client) GetSchema(ctx context.Context, version string) (*Schema, error) {
	resp, err := c.sdk.Ledger.V2.GetSchema(ctx, operations.V2GetSchemaRequest{
		Ledger:  c.ledger,
		Version: version,
	})
	if err != nil {
		if isEmptyResponse(err) || isNotFound(err) {
			return nil, fmt.Errorf("schema version %q not found", version)
		}
		return nil, err
	}

	schemaResp := resp.GetV2SchemaResponse()
	if schemaResp == nil {
		return nil, fmt.Errorf("empty response for version %q", version)
	}

	s := schemaFromSDK(schemaResp.Data)
	return &s, nil
}

func schemaFromSDK(s components.V2Schema) Schema {
	// Convert typed maps to map[string]any for uniform JSON output.
	chartRaw, _ := json.Marshal(s.Chart)
	txRaw, _ := json.Marshal(s.Transactions)
	qRaw, _ := json.Marshal(s.Queries)

	var chart, tx, q map[string]any
	json.Unmarshal(chartRaw, &chart)
	json.Unmarshal(txRaw, &tx)
	json.Unmarshal(qRaw, &q)

	return Schema{
		Version:      s.Version,
		CreatedAt:    s.CreatedAt,
		Chart:        chart,
		Transactions: tx,
		Queries:      q,
	}
}
