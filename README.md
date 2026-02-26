# Formance Chart Sync

Validates Formance chart YAML files against the v4 schema and pushes them to the Ledger schemas API.

Works as a **GitHub Action**, a **Docker image**, or a **standalone CLI**.

## Quick Start — GitHub Action

Add this workflow to any repository that contains Formance chart files:

```yaml
# .github/workflows/chart-sync.yml
name: Chart Sync

on:
  push:
    branches: [main]
    paths: ["charts/**"]
  pull_request:
    paths: ["charts/**"]

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: thierrycoopman/formance-chart-sync@v1
        with:
          client_id:     ${{ secrets.FORMANCE_CLIENT_ID }}
          client_secret: ${{ secrets.FORMANCE_CLIENT_SECRET }}
          server_url:    ${{ secrets.FORMANCE_SERVER_URL }}
          chart_glob:    "charts/**/*.yaml"
          dry_run:       ${{ github.event_name == 'pull_request' }}
```

On **pull requests**, charts are validated but not pushed (`dry_run: true`).
On **push to main**, charts are validated and pushed to the Ledger.

### Action Inputs

| Input | Required | Default | Description |
|-------|----------|---------|-------------|
| `client_id` | Yes | — | Formance OAuth2 client ID |
| `client_secret` | Yes | — | Formance OAuth2 client secret |
| `server_url` | Yes | — | Formance API base URL |
| `ledger` | No | from chart | Target ledger (defaults to `ledger.name` in chart YAML) |
| `version` | No | from chart | Schema version prefix (defaults to `version` in chart YAML) |
| `chart_glob` | No | `**/*.chart.yaml` | Glob pattern to match chart files |
| `dry_run` | No | `false` | Validate without pushing |
| `force` | No | `false` | Skip Ledger version check |

### Required Secrets

Create these in your repository settings under **Settings > Secrets and variables > Actions**:

| Secret | Description |
|--------|-------------|
| `FORMANCE_CLIENT_ID` | OAuth2 client ID from the Formance dashboard |
| `FORMANCE_CLIENT_SECRET` | OAuth2 client secret from the Formance dashboard |
| `FORMANCE_SERVER_URL` | Your Formance environment URL (e.g. `https://org.eu-west-1.formance.cloud`) |

## Step-by-Step: Adding Chart Sync to a Repository

### 1. Create a chart file

Place your chart YAML in the repository. The file must conform to the [v4 chart schema](schema/chart_v4.schema.json).

```yaml
# charts/payments.yaml
version: v1
createdAt: 2026-01-01T00:00:00Z

ledger:
  name: payments-ledger

chart:
  users:
    $userid:
      .self: {}
      .metadata:
        nature: { default: "operating" }
      main:
        .self: {}
      savings:
        .self: {}

transactions:
  DEPOSIT:
    runtime: experimental-interpreter
    script: |
      vars {
        account $userid
        monetary $amount
      }
      send $amount (
        source = @world
        destination = @users:$userid:main
      )
```

Key rules:
- **`ledger.name`** is required — it's the target ledger on the Formance stack
- **`version`** is used as the schema version prefix when pushing (e.g. `v1` becomes `v1+repo.branch.file.sha.hash`)
- **`.self: {}`** is required on any segment that has `.metadata` — it marks the segment as a bookable account
- **`runtime: experimental-interpreter`** is required when scripts use `$variable` interpolation in addresses

### 2. Add the workflow

Create `.github/workflows/chart-sync.yml`:

```yaml
name: Chart Sync

on:
  push:
    branches: [main]
    paths: ["charts/**"]
  pull_request:
    paths: ["charts/**"]

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: thierrycoopman/formance-chart-sync@v1
        with:
          client_id:     ${{ secrets.FORMANCE_CLIENT_ID }}
          client_secret: ${{ secrets.FORMANCE_CLIENT_SECRET }}
          server_url:    ${{ secrets.FORMANCE_SERVER_URL }}
          chart_glob:    "charts/**/*.yaml"
          dry_run:       ${{ github.event_name == 'pull_request' }}
```

Adjust `paths` and `chart_glob` to match where your chart files live.

### 3. Add secrets

Go to your repository **Settings > Secrets and variables > Actions** and add:
- `FORMANCE_CLIENT_ID`
- `FORMANCE_CLIENT_SECRET`
- `FORMANCE_SERVER_URL`

### 4. Push

Commit and push. The action will validate your chart on PRs and push it to the Ledger on merge to main.

### What happens on push

1. Chart YAML is validated against the v4 schema
2. The Ledger-compatible payload is extracted (stripping v4-only fields like `assets`, `business`, `placeholders`)
3. The extracted payload is validated against the Ledger API schema
4. If the target ledger doesn't exist, it's created automatically
5. The schema is pushed with a version string that includes provenance metadata
6. All installed schema versions are listed after a successful push

## CLI Usage

### From Source

```bash
go build -o chart-sync .

# Validate a chart locally (no network required)
./chart-sync validate charts/payments.yaml

# Push a chart
SERVER_URL=https://org.eu-west-1.formance.cloud \
CLIENT_ID=... \
CLIENT_SECRET=... \
CHART_GLOB="charts/*.yaml" \
FORCE=true \
./chart-sync

# List installed schemas
./chart-sync list \
  --server-url=https://org.eu-west-1.formance.cloud \
  --client-id=... --client-secret=... \
  --ledger=payments-ledger

# Get a specific schema version
./chart-sync get "v1+main.abc1234.f3e9a0b1" \
  --server-url=https://org.eu-west-1.formance.cloud \
  --client-id=... --client-secret=... \
  --ledger=payments-ledger
```

### Commands

```
chart-sync <command> [options]

Commands:
  push       Validate and push chart files (default when no command given)
  list       List installed schema versions on the remote Ledger
  get        Fetch a specific schema by version
  validate   Validate a local chart file against the v4 schema
```

#### `push` (default)

Reads configuration from environment variables. This is the default command when no subcommand is given — used by the GitHub Action.

| Env Var | Required | Description |
|---------|----------|-------------|
| `SERVER_URL` | Yes | Formance API base URL |
| `CLIENT_ID` | Yes | OAuth2 client ID |
| `CLIENT_SECRET` | Yes | OAuth2 client secret |
| `LEDGER` | No | Target ledger (defaults to chart's `ledger.name`) |
| `VERSION` | No | Schema version prefix (defaults to chart's `version`) |
| `CHART_GLOB` | No | File glob (default: `**/*.chart.yaml`) |
| `DRY_RUN` | No | `true` to validate only |
| `FORCE` | No | `true` to skip Ledger version check |

After a successful push, the tool automatically lists all installed schema versions.

#### `validate <file...>`

Validates one or more chart YAML files against both the v4 chart schema and the Ledger API schema. No network access required.

```bash
chart-sync validate charts/*.yaml
chart-sync validate --schema path/to/schema.json charts/payments.yaml
chart-sync validate --json charts/payments.yaml
```

#### `list`

Lists all schema versions installed on the remote Ledger.

```bash
chart-sync list --server-url=... --ledger=...
chart-sync list --server-url=... --ledger=... --json
```

All flags fall back to environment variables (`SERVER_URL`, `CLIENT_ID`, `CLIENT_SECRET`, `LEDGER`).

#### `get <version>`

Fetches a single schema by version. Always outputs JSON.

```bash
chart-sync get "v1+main.abc1234.f3e9a0b1" --server-url=... --ledger=...
```

## Chart YAML Format

Charts use the [v4 schema](schema/chart_v4.schema.json). Key sections:

```yaml
version: v1                    # used as schema version prefix on push
createdAt: 2026-01-01T00:00:00Z

ledger:
  name: my-ledger              # required: target ledger on the Formance stack

# Account tree (segments form the address hierarchy)
chart:
  users:
    $userid:
      .self: {}                # marks this as a bookable account
      .metadata:               # requires .self on the same segment
        nature: { default: "operating" }
      main:
        .self: {}
      savings:
        .self: {}

# Transaction templates with Numscript
transactions:
  DEPOSIT:
    runtime: experimental-interpreter  # required for $variable interpolation
    script: |
      vars {
        account $userid
        monetary $amount
      }
      send $amount (
        source = @world
        destination = @users:$userid:main
      )

# Optional: query definitions
queries:
  user_balance:
    resource: accounts
    body: { match: { "metadata[userid]": "$userid" } }
```

### Important rules

- **`.self: {}`** must be present on any segment that has `.metadata` — the Ledger rejects `.metadata` on non-account segments
- **`runtime: experimental-interpreter`** is required when scripts use `$variable` interpolation in account addresses (e.g. `@users:$userid:main`)
- **No duplicate variable segments** — you cannot have two `$`-prefixed keys at the same level (e.g. `$a` and `$b` as siblings)
- **Metadata defaults** are stringified automatically — booleans become `"true"`/`"false"`, numbers become decimal strings, objects become JSON strings

### Two-stage validation

The tool validates in two stages:

1. **v4 schema** — validates the full chart YAML (including `assets`, `business`, `placeholders`, etc.)
2. **Ledger API schema** — validates the extracted payload that will be sent to the Ledger API

The extraction strips v4-only fields that the Ledger doesn't accept:
- From transactions: only `script`, `description`, `runtime` are kept
- From metadata: only `default` is kept (v4 fields like `type`, `pattern`, `enum` are stripped)
- Top-level fields like `assets`, `business`, `placeholders`, `ledger` are removed

## Version String

The push command builds a version string with provenance metadata:

```
{version}+{repo}.{branch}.{filepath}.{commitSHA7}.{fileHash8}
```

Example: `v1+org-my-repo.main.charts-payments.yaml.abc1234.f3e9a0b1`

This ensures every push is traceable to its source repository, branch, file, and commit.

When `VERSION` is not set, the `version` field from the chart YAML is used as the prefix.

## Publishing the Action

For other repositories to use `thierrycoopman/formance-chart-sync@v1`, you need a `v1` tag:

```bash
# Create the initial release tag
git tag v1.0.0
git push origin v1.0.0

# Create the floating v1 tag that consumers reference
git tag -f v1 v1.0.0
git push -f origin v1
```

On subsequent releases, update both tags:

```bash
git tag v1.1.0
git push origin v1.1.0

# Move the floating v1 tag forward
git tag -f v1 v1.1.0
git push -f origin v1
```

The `v1` floating tag is the convention for GitHub Actions — consumers pin to `@v1` and get the latest `v1.x.x` release automatically.

## Architecture

```
formance-chart-sync/
  main.go                                CLI dispatch and GitHub Actions integration
  internal/
    chart/                               Schema validation, Ledger extraction
    convert/                             YAML-to-JSON conversion (anchors, merge keys)
    push/                                Formance SDK client (push, list, get)
    env/                                 Environment variable configuration
    changed/                             GitHub push event file detection
  schema/
    chart_v4.schema.json                 JSON Schema for full chart validation
    ledger_v2_schema_data.schema.json    JSON Schema for Ledger API payload validation
  testdata/                              Example charts for testing
  action.yml                             GitHub Action definition
  Dockerfile                             Multi-stage build (scratch runtime)
```

## Development

```bash
# Run tests
go test -race ./...

# Build
go build -o chart-sync .

# Validate a chart locally
./chart-sync validate testdata/starter-chart.yaml

# Push to staging
SERVER_URL=https://your-env.staging.formance.cloud \
CLIENT_ID=... CLIENT_SECRET=... \
CHART_GLOB="testdata/starter-chart.yaml" \
FORCE=true \
./chart-sync
```
