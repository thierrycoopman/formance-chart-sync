package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/formancehq/formance-chart-sync/internal/changed"
	"github.com/formancehq/formance-chart-sync/internal/chart"
	"github.com/formancehq/formance-chart-sync/internal/env"
	"github.com/formancehq/formance-chart-sync/internal/push"
)

func main() {
	if err := dispatch(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func dispatch() error {
	if len(os.Args) < 2 {
		// No subcommand — default to push (backward compat for GitHub Actions).
		return runPush()
	}

	switch os.Args[1] {
	case "push":
		return runPush()
	case "list":
		return runList(os.Args[2:])
	case "get":
		return runGet(os.Args[2:])
	case "validate":
		return runValidate(os.Args[2:])
	case "--help", "-h", "help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: chart-sync <command> [options]

Commands:
  push       Validate and push chart files to Formance (default)
  list       List installed schema versions
  get        Fetch a specific schema by version
  validate   Validate a local chart file

Run 'chart-sync <command> --help' for command-specific options.
`)
}

// --- Connection flags (shared by list/get) ---

type connFlags struct {
	serverURL    string
	clientID     string
	clientSecret string
	ledger       string
	jsonOutput   bool
	force        bool
}

func addConnFlags(fs *flag.FlagSet, cf *connFlags) {
	fs.StringVar(&cf.serverURL, "server-url", os.Getenv("SERVER_URL"), "Formance API URL (env: SERVER_URL)")
	fs.StringVar(&cf.clientID, "client-id", os.Getenv("CLIENT_ID"), "OAuth client ID (env: CLIENT_ID)")
	fs.StringVar(&cf.clientSecret, "client-secret", os.Getenv("CLIENT_SECRET"), "OAuth client secret (env: CLIENT_SECRET)")
	fs.StringVar(&cf.ledger, "ledger", os.Getenv("LEDGER"), "Target ledger name (env: LEDGER)")
	fs.BoolVar(&cf.jsonOutput, "json", false, "Output as JSON")
	fs.BoolVar(&cf.force, "force", strings.EqualFold(os.Getenv("FORCE"), "true"), "Skip Ledger version check (env: FORCE)")
}

func (cf *connFlags) validate() error {
	var missing []string
	if cf.serverURL == "" {
		missing = append(missing, "--server-url / SERVER_URL")
	}
	if cf.ledger == "" {
		missing = append(missing, "--ledger / LEDGER")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (cf *connFlags) client() *push.Client {
	return push.New(cf.serverURL, cf.clientID, cf.clientSecret, cf.ledger, "")
}

// printVersionInfo fetches and prints component versions alongside the SDK
// version. Returns the VersionInfo for further checks (e.g. schema support).
func printVersionInfo(ctx context.Context, c *push.Client) *push.VersionInfo {
	vi, err := c.GetVersionInfo(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch server versions: %v\n", err)
		fmt.Fprintf(os.Stderr, "SDK: %s\n", c.SDKVersion())
		return nil
	}
	fmt.Fprintf(os.Stderr, "Env: %s (%s) | Ledger: %s | SDK: %s\n",
		vi.Env, vi.Region, vi.LedgerVersion(), vi.SDKVersion)
	return vi
}

// checkSchemasSupport verifies the server supports the schemas API and returns
// an error if the Ledger version is too old. Pass force=true to downgrade the
// error to a warning (useful for staging environments with commit-SHA versions).
func checkSchemasSupport(vi *push.VersionInfo, force bool) error {
	if vi == nil {
		return nil // can't check — let the operation try and fail with a clear 404 error
	}
	ok, ver := vi.SupportsSchemas()
	if !ok {
		msg := fmt.Sprintf("Ledger %s does not support the schemas API (requires >= %s)", ver, push.MinSchemasVersion)
		if force {
			fmt.Fprintf(os.Stderr, "Warning: %s — proceeding anyway (--force)\n", msg)
			return nil
		}
		return fmt.Errorf("%s — use --force or FORCE=true to override", msg)
	}
	return nil
}

// checkLedger verifies auth and connectivity, printing ledger status.
func checkLedger(ctx context.Context, c *push.Client) error {
	info, err := c.CheckLedger(ctx)
	if err != nil {
		return err
	}
	if info.Exists {
		fmt.Fprintf(os.Stderr, "Ledger %q: OK\n", info.Name)
	} else {
		fmt.Fprintf(os.Stderr, "Ledger %q: not found\n", info.Name)
	}
	return nil
}

// --- list command ---

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var cf connFlags
	addConnFlags(fs, &cf)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cf.validate(); err != nil {
		return err
	}

	ctx := context.Background()
	c := cf.client()

	vi := printVersionInfo(ctx, c)
	if err := checkSchemasSupport(vi, cf.force); err != nil {
		return err
	}

	if err := checkLedger(ctx, c); err != nil {
		return err
	}

	schemas, err := c.ListSchemas(ctx)
	if err != nil {
		return fmt.Errorf("listing schemas: %w", err)
	}

	if cf.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(schemas)
	}

	return printSchemaTable(cf.ledger, schemas)
}

// --- get command ---

func runGet(args []string) error {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	var cf connFlags
	addConnFlags(fs, &cf)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cf.validate(); err != nil {
		return err
	}

	version := fs.Arg(0)
	if version == "" {
		return errors.New("usage: chart-sync get <version> [--server-url ...] [--ledger ...]")
	}

	ctx := context.Background()
	c := cf.client()

	vi := printVersionInfo(ctx, c)
	if err := checkSchemasSupport(vi, cf.force); err != nil {
		return err
	}

	if err := checkLedger(ctx, c); err != nil {
		return err
	}

	schema, err := c.GetSchema(ctx, version)
	if err != nil {
		return fmt.Errorf("getting schema %q: %w", version, err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(schema)
}

// --- validate command ---

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	schemaPath := fs.String("schema", "", "Path to chart_v4.schema.json (auto-detected if not set)")
	jsonOutput := fs.Bool("json", false, "Output validation result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	files := fs.Args()
	if len(files) == 0 {
		return errors.New("usage: chart-sync validate <file...> [--schema path]")
	}

	// Auto-detect schema path.
	sp := *schemaPath
	if sp == "" {
		sp = cmp.Or(os.Getenv("SCHEMA_PATH"), findSchemaPath())
	}
	if err := chart.LoadSchema(sp); err != nil {
		return fmt.Errorf("loading JSON schema from %s: %w", sp, err)
	}

	// Load Ledger API schema for extracted payload validation.
	lsp := cmp.Or(os.Getenv("LEDGER_SCHEMA_PATH"), findLedgerSchemaPath())
	if err := chart.LoadLedgerSchema(lsp); err != nil {
		return fmt.Errorf("loading Ledger schema from %s: %w", lsp, err)
	}

	hasErrors := false

	for _, f := range files {
		result, err := chart.Validate(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: error: %v\n", f, err)
			hasErrors = true
			continue
		}

		if !result.Valid {
			if *jsonOutput {
				out := map[string]any{"file": f, "valid": false, "errors": result.Errors}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				enc.Encode(out)
			} else {
				fmt.Printf("%s: FAILED\n", f)
				for _, e := range result.Errors {
					fmt.Printf("  - %s\n", e)
				}
			}
			hasErrors = true
			continue
		}

		// Also validate Ledger API compatibility.
		ledgerJSON, extractErr := result.ExtractLedgerSchema()
		var ledgerErrs []string
		if extractErr != nil {
			ledgerErrs = append(ledgerErrs, extractErr.Error())
		} else if valErr := chart.ValidateLedgerPayload(ledgerJSON); valErr != nil {
			ledgerErrs = append(ledgerErrs, valErr.Error())
		}

		if *jsonOutput {
			out := map[string]any{"file": f, "valid": len(ledgerErrs) == 0}
			if len(ledgerErrs) > 0 {
				out["ledgerErrors"] = ledgerErrs
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(out)
			if len(ledgerErrs) > 0 {
				hasErrors = true
			}
		} else if len(ledgerErrs) > 0 {
			fmt.Printf("%s: FAILED (Ledger compatibility)\n", f)
			for _, e := range ledgerErrs {
				fmt.Printf("  - %s\n", e)
			}
			hasErrors = true
		} else {
			accounts, txns := countFields(result.JSON)
			fmt.Printf("%s: OK (%d accounts, %d transactions)\n", f, accounts, txns)
		}
	}

	if hasErrors {
		return errors.New("validation failed")
	}
	return nil
}

func countFields(chartJSON []byte) (accounts, transactions int) {
	var data map[string]any
	if err := json.Unmarshal(chartJSON, &data); err != nil {
		return 0, 0
	}
	if c, ok := data["chart"].(map[string]any); ok {
		accounts = countAccounts(c)
	}
	if t, ok := data["transactions"].(map[string]any); ok {
		transactions = len(t)
	}
	return accounts, transactions
}

// countAccounts recursively counts bookable accounts (segments with ".self")
// in a chart of accounts tree.
func countAccounts(segment map[string]any) int {
	count := 0
	for key, val := range segment {
		if key == ".self" {
			count++
			continue
		}
		// Skip other dot-keys (.metadata, .pattern, .rules).
		if strings.HasPrefix(key, ".") {
			continue
		}
		if child, ok := val.(map[string]any); ok {
			count += countAccounts(child)
		}
	}
	return count
}

// findSchemaPath looks for the schema file in common locations.
func findSchemaPath() string {
	candidates := []string{
		"schema/chart_v4.schema.json",
		"/schema/chart_v4.schema.json",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0] // fallback, will produce a clear error
}

// findLedgerSchemaPath looks for the Ledger API schema file in common locations.
func findLedgerSchemaPath() string {
	candidates := []string{
		"schema/ledger_v2_schema_data.schema.json",
		"/schema/ledger_v2_schema_data.schema.json",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// --- push command (original workflow) ---

func runPush() error {
	ctx := context.Background()

	cfg, err := env.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ghNotice("Target endpoint: %s", cfg.ServerURL)
	if cfg.Version != "" {
		ghNotice("Ledger: %s, Version: %s", cmp.Or(cfg.Ledger, "(from chart)"), cfg.Version)
	} else {
		ghNotice("Ledger: %s, Version: (from chart)", cmp.Or(cfg.Ledger, "(from chart)"))
	}
	if cfg.DryRun {
		ghNotice("Dry run mode — charts will be validated but not pushed")
	}

	schemaPath := cmp.Or(os.Getenv("SCHEMA_PATH"), findSchemaPath())
	if err := chart.LoadSchema(schemaPath); err != nil {
		return fmt.Errorf("loading JSON schema: %w", err)
	}

	ledgerSchemaPath := cmp.Or(os.Getenv("LEDGER_SCHEMA_PATH"), findLedgerSchemaPath())
	if err := chart.LoadLedgerSchema(ledgerSchemaPath); err != nil {
		return fmt.Errorf("loading Ledger schema: %w", err)
	}

	// Discover all chart files matching the glob under the workspace.
	allCharts, err := doublestar.FilepathGlob(
		filepath.Join(cfg.Workspace, cfg.ChartGlob),
	)
	if err != nil {
		return fmt.Errorf("globbing chart files: %w", err)
	}

	// Narrow to files changed in this push event (nil = no filter).
	changedFiles := changed.Files(cfg.EventPath, cfg.Workspace)
	charts := intersect(allCharts, changedFiles)

	if len(charts) == 0 {
		ghNotice("No matching chart files changed. Nothing to do.")
		return nil
	}

	ghNotice("Processing %d chart file(s)", len(charts))

	pusher := push.New(cfg.ServerURL, cfg.ClientID, cfg.ClientSecret, cfg.Ledger, cfg.Version)

	// Print component and SDK version info and check schemas API support.
	if !cfg.DryRun {
		vi, viErr := pusher.GetVersionInfo(ctx)
		if viErr != nil {
			ghNotice("Warning: could not fetch server versions: %v (SDK %s)", viErr, pusher.SDKVersion())
		} else {
			ghNotice("Env: %s (%s) | Ledger: %s | SDK: %s", vi.Env, vi.Region, vi.LedgerVersion(), vi.SDKVersion)
			if err := checkSchemasSupport(vi, cfg.Force); err != nil {
				return err
			}
		}
	}

	// Track which ledgers we've already checked to avoid redundant API calls.
	checkedLedgers := make(map[string]bool)
	ledgerHashes := make(map[string]map[string]bool)

	var successes []chartSuccess
	var failures []chartFailure

	prov := push.Provenance{
		Repository: cfg.Repository,
		Branch:     cfg.Branch,
		CommitSHA:  cfg.CommitSHA,
	}

	for _, chartPath := range charts {
		rel := relPath(cfg.Workspace, chartPath)
		ghStartGroup(rel)

		// Read raw YAML for provenance hash.
		rawYAML, readErr := os.ReadFile(chartPath)
		if readErr != nil {
			ghFileError(rel, readErr.Error())
			failures = append(failures, chartFailure{rel, readErr})
			ghEndGroup()
			continue
		}

		result, validateErr := chart.Validate(chartPath)
		if validateErr != nil {
			ghFileError(rel, validateErr.Error())
			failures = append(failures, chartFailure{rel, validateErr})
			ghEndGroup()
			continue
		}

		if !result.Valid {
			msg := fmt.Sprintf("schema validation failed:\n  - %s", result.ErrorSummary())
			ghFileError(rel, msg)
			failures = append(failures, chartFailure{rel, errors.New(msg)})
			ghEndGroup()
			continue
		}
		ghNotice("Schema valid")

		// Extract ledger name from chart (always required in the YAML).
		chartLedger, ledgerErr := result.ExtractLedgerName()
		if ledgerErr != nil {
			ghFileError(rel, ledgerErr.Error())
			failures = append(failures, chartFailure{rel, ledgerErr})
			ghEndGroup()
			continue
		}

		// Resolve which ledger to use for this chart.
		targetLedger := cfg.Ledger
		if targetLedger == "" {
			// No LEDGER configured — use the name from the chart.
			targetLedger = chartLedger
			ghNotice("Using ledger from chart: %s", targetLedger)
		} else if chartLedger != targetLedger {
			// LEDGER overrides the chart name — warn but proceed.
			ghNotice("Warning: chart ledger name %q differs from configured ledger %q — using %q", chartLedger, targetLedger, targetLedger)
		}
		ghNotice("Ledger: %s", targetLedger)

		// Point the pusher at this chart's ledger.
		pusher.SetLedger(targetLedger)

		// Check ledger once per unique name — create it if it doesn't exist.
		if !cfg.DryRun && !checkedLedgers[targetLedger] {
			info, checkErr := pusher.CheckLedger(ctx)
			if checkErr != nil {
				ghFileError(rel, checkErr.Error())
				failures = append(failures, chartFailure{rel, checkErr})
				ghEndGroup()
				continue
			}
			if info.Exists {
				ghNotice("Ledger %q confirmed", targetLedger)
			} else {
				ghNotice("Ledger %q not found — creating", targetLedger)
				if createErr := pusher.CreateLedger(ctx); createErr != nil {
					ghFileError(rel, fmt.Sprintf("creating ledger %q: %v", targetLedger, createErr))
					failures = append(failures, chartFailure{rel, createErr})
					ghEndGroup()
					continue
				}
				ghNotice("Ledger %q created", targetLedger)
			}
			checkedLedgers[targetLedger] = true

			schemas, listErr := pusher.ListSchemas(ctx)
			if listErr != nil {
				ghNotice("Warning: could not list existing schemas for dedup: %v — will push anyway", listErr)
			} else {
				ledgerHashes[targetLedger] = push.CollectFileHashes(schemas)
				ghNotice("Found %d existing schema(s) with %d unique file hash(es)",
					len(schemas), len(ledgerHashes[targetLedger]))
			}
		}

		// Extract Ledger-compatible schema (chart, transactions, queries only).
		ledgerJSON, extractErr := result.ExtractLedgerSchema()
		if extractErr != nil {
			ghFileError(rel, extractErr.Error())
			failures = append(failures, chartFailure{rel, extractErr})
			ghEndGroup()
			continue
		}
		ghNotice("Extracted Ledger schema")

		// Validate extracted JSON against the Ledger API schema.
		if valErr := chart.ValidateLedgerPayload(ledgerJSON); valErr != nil {
			ghFileError(rel, valErr.Error())
			failures = append(failures, chartFailure{rel, valErr})
			ghEndGroup()
			continue
		}
		ghNotice("Ledger payload validated")

		// Resolve base version: env override or chart's version field.
		if cfg.Version != "" {
			pusher.SetBaseVersion(cfg.Version)
		} else {
			chartVersion, verErr := result.ExtractVersion()
			if verErr != nil {
				ghFileError(rel, fmt.Sprintf("no VERSION set and chart has no version field: %v", verErr))
				failures = append(failures, chartFailure{rel, verErr})
				ghEndGroup()
				continue
			}
			pusher.SetBaseVersion(chartVersion)
			ghNotice("Using version from chart: %s", chartVersion)
		}

		// Build version with provenance metadata.
		prov.FilePath = rel
		version := pusher.BuildVersion(rawYAML, prov)
		ghNotice("Version: %s", version)

		fileHash := push.FileHash(rawYAML)
		if hashes, ok := ledgerHashes[targetLedger]; ok && hashes[fileHash] {
			ghNotice("Skipped (file hash %s already exists in ledger %q)", fileHash, targetLedger)
			successes = append(successes, chartSuccess{rel, version + " (unchanged)", targetLedger})
			ghEndGroup()
			continue
		}

		if cfg.DryRun {
			ghNotice("Dry run — skipping push (file hash: %s)", fileHash)
			dumpPayload(rel, version, ledgerJSON)
		} else {
			if pushErr := pusher.Push(ctx, version, ledgerJSON); pushErr != nil {
				// Dump the payload so the user can debug whether the
				// error comes from the YAML content or the transformation.
				dumpPayload(rel, version, ledgerJSON)
				ghFileError(rel, pushErr.Error())
				failures = append(failures, chartFailure{rel, pushErr})
				ghEndGroup()
				continue
			}
			ghNotice("Pushed")
		}

		successes = append(successes, chartSuccess{rel, version, targetLedger})
		ghEndGroup()
	}

	// After successful pushes, list schemas for each unique ledger.
	ledgerSchemas := make(map[string][]push.Schema)
	if !cfg.DryRun && len(successes) > 0 && len(failures) == 0 {
		// Collect unique ledgers in order of first appearance.
		seen := make(map[string]bool)
		var ledgers []string
		for _, s := range successes {
			if !seen[s.ledger] {
				seen[s.ledger] = true
				ledgers = append(ledgers, s.ledger)
			}
		}

		for _, ledger := range ledgers {
			fmt.Println()
			ghNotice("Listing installed schemas for ledger %q", ledger)
			pusher.SetLedger(ledger)
			listed, listErr := pusher.ListSchemas(ctx)
			if listErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not list schemas for %q: %v\n", ledger, listErr)
				continue
			}
			ledgerSchemas[ledger] = listed
			if err := printSchemaTable(ledger, listed); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not print schema list: %v\n", err)
			}
		}
	}

	writeSummary(successes, failures, ledgerSchemas, cfg.ServerURL, cfg.DryRun)

	if len(failures) > 0 {
		return fmt.Errorf("%d chart(s) failed to process", len(failures))
	}
	return nil
}

// printSchemaTable prints a list of schemas as a formatted table for the given ledger.
func printSchemaTable(ledger string, schemas []push.Schema) error {
	if len(schemas) == 0 {
		fmt.Printf("No schemas found for ledger %q.\n", ledger)
		return nil
	}

	fmt.Printf("Ledger: %s\n", ledger)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tCREATED\tACCOUNTS\tTRANSACTIONS\tQUERIES")
	for _, s := range schemas {
		created := s.CreatedAt.Format(time.DateTime)
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n",
			s.Version,
			created,
			countAccounts(s.Chart),
			len(s.Transactions),
			len(s.Queries),
		)
	}
	return w.Flush()
}

// --- Helpers ---

// intersect returns elements of all that are present in filter.
// If filter is nil, all elements are returned (no filtering applied).
func intersect(all, filter []string) []string {
	if filter == nil {
		return all
	}
	set := make(map[string]struct{}, len(filter))
	for _, f := range filter {
		set[f] = struct{}{}
	}
	var out []string
	for _, f := range all {
		if _, ok := set[f]; ok {
			out = append(out, f)
		}
	}
	return out
}

func relPath(workspace, abs string) string {
	rel, err := filepath.Rel(workspace, abs)
	if err != nil {
		return abs
	}
	return rel
}

// --- GitHub Actions workflow command helpers ---

func ghNotice(format string, args ...any) {
	fmt.Printf("::notice::"+format+"\n", args...)
}

func ghFileError(file, msg string) {
	fmt.Printf("::error file=%s::%s\n", file, escapeData(msg))
}

func ghStartGroup(name string) { fmt.Printf("::group::%s\n", name) }
func ghEndGroup()              { fmt.Println("::endgroup::") }

func escapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// dumpPayload prints the extracted Ledger JSON payload to stderr so the user
// can inspect exactly what was sent (or would be sent) to the schemas API.
func dumpPayload(rel, version string, ledgerJSON []byte) {
	var formatted json.RawMessage = ledgerJSON
	pretty, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		pretty = ledgerJSON
	}
	fmt.Fprintf(os.Stderr, "\n=== PAYLOAD for %s (version: %s) ===\n%s\n=== END PAYLOAD ===\n\n", rel, version, pretty)
}

type chartSuccess struct {
	rel     string
	version string
	ledger  string
}

type chartFailure struct {
	rel string
	err error
}

// parseFormanceURL extracts organization and stack from a Formance Cloud URL.
// Pattern: https://{org}-{stack}.{env}.formance.cloud
// Returns empty strings if the URL doesn't match.
func parseFormanceURL(serverURL string) (org, stack string) {
	serverURL = strings.TrimRight(serverURL, "/")
	// Strip scheme.
	host := serverURL
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	// Strip port and path.
	if idx := strings.IndexAny(host, ":/"); idx >= 0 {
		host = host[:idx]
	}
	// Expect {org}-{stack}.*.formance.cloud
	if !strings.HasSuffix(host, ".formance.cloud") {
		return "", ""
	}
	subdomain, _, _ := strings.Cut(host, ".")
	// Split on the last hyphen: org may contain hyphens, stack does not.
	idx := strings.LastIndex(subdomain, "-")
	if idx <= 0 || idx == len(subdomain)-1 {
		return "", ""
	}
	return subdomain[:idx], subdomain[idx+1:]
}

func writeSummary(successes []chartSuccess, failures []chartFailure, ledgerSchemas map[string][]push.Schema, serverURL string, dryRun bool) {
	summaryPath := os.Getenv("GITHUB_STEP_SUMMARY")
	if summaryPath == "" {
		return
	}
	f, err := os.OpenFile(summaryPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// --- Target info ---
	fmt.Fprintln(f, "## Chart Sync Results")
	fmt.Fprintln(f)

	org, stack := parseFormanceURL(serverURL)
	if org != "" {
		fmt.Fprintln(f, "| | |")
		fmt.Fprintln(f, "|---|---|")
		fmt.Fprintf(f, "| **Organization** | `%s` |\n", org)
		fmt.Fprintf(f, "| **Stack** | `%s` |\n", stack)
		fmt.Fprintln(f)
	}

	// --- Per-file results with ledger column ---
	label := "Synced"
	if dryRun {
		label = "Validated (dry run)"
	}

	fmt.Fprintln(f, "| File | Ledger | Version | Status |")
	fmt.Fprintln(f, "|------|--------|---------|--------|")
	for _, s := range successes {
		status := label
		if strings.HasSuffix(s.version, " (unchanged)") {
			status = "Skipped (unchanged)"
		}
		fmt.Fprintf(f, "| `%s` | `%s` | `%s` | %s |\n", s.rel, s.ledger, s.version, status)
	}
	for _, fail := range failures {
		escaped := strings.ReplaceAll(fail.err.Error(), "|", "\\|")
		fmt.Fprintf(f, "| `%s` | — | — | %s |\n", fail.rel, escaped)
	}

	// --- Installed schemas grouped by ledger ---
	if len(ledgerSchemas) > 0 {
		// Sort ledger names for deterministic output.
		ledgers := make([]string, 0, len(ledgerSchemas))
		for l := range ledgerSchemas {
			ledgers = append(ledgers, l)
		}
		slices.Sort(ledgers)

		fmt.Fprintln(f)
		fmt.Fprintln(f, "## Installed Schemas")

		for _, ledger := range ledgers {
			schemas := ledgerSchemas[ledger]
			if len(schemas) == 0 {
				continue
			}
			fmt.Fprintln(f)
			fmt.Fprintf(f, "### `%s`\n", ledger)
			fmt.Fprintln(f)
			fmt.Fprintln(f, "| Version | Created | Accounts | Transactions | Queries |")
			fmt.Fprintln(f, "|---------|---------|----------|--------------|---------|")
			for _, s := range schemas {
				created := s.CreatedAt.Format(time.DateTime)
				fmt.Fprintf(f, "| `%s` | %s | %d | %d | %d |\n",
					s.Version,
					created,
					countAccounts(s.Chart),
					len(s.Transactions),
					len(s.Queries),
				)
			}
		}
	}
}
