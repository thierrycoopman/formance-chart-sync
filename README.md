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
      - uses: formancehq/formance-chart-sync@v1
        with:
          client_id:     ${{ secrets.FORMANCE_CLIENT_ID }}
          client_secret: ${{ secrets.FORMANCE_CLIENT_SECRET }}
          server_url:    ${{ secrets.FORMANCE_SERVER_URL }}
          version:       v1
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
| `version` | Yes | — | Schema version prefix (e.g. `v1`) |
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
version: "4"
date: "2025-09-16"

ledger:
  name: payments-ledger

chart:
  users:
    $userid:
      main: {}
      savings: {}

transactions:
  DEPOSIT:
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

### 2. Add the workflow

Create `.github/workflows/chart-sync.yml` as shown in [Quick Start](#quick-start--github-action).

### 3. Add secrets

Go to **Settings > Secrets and variables > Actions** and add:
- `FORMANCE_CLIENT_ID`
- `FORMANCE_CLIENT_SECRET`
- `FORMANCE_SERVER_URL`

### 4. Push

Commit and push. The action will validate your chart on PRs and push it to the Ledger on merge to main.

## CLI Usage

### Docker

```bash
# Validate a chart locally
docker run --rm -v "$(pwd):/work" \
  ghcr.io/formancehq/formance-chart-sync \
  validate /work/charts/payments.yaml

# Push a chart
docker run --rm -v "$(pwd):/work" \
  -e CLIENT_ID="..." \
  -e CLIENT_SECRET="..." \
  -e SERVER_URL="https://org.eu-west-1.formance.cloud" \
  -e LEDGER="payments-ledger" \
  -e VERSION="v1" \
  -e CHART_GLOB="/work/charts/*.yaml" \
  ghcr.io/formancehq/formance-chart-sync

# List installed schemas
docker run --rm \
  ghcr.io/formancehq/formance-chart-sync \
  list --server-url=https://org.eu-west-1.formance.cloud \
       --client-id=... --client-secret=... \
       --ledger=payments-ledger

# Get a specific schema version
docker run --rm \
  ghcr.io/formancehq/formance-chart-sync \
  get "v1+main.abc1234.f3e9a0b1" \
       --server-url=https://org.eu-west-1.formance.cloud \
       --client-id=... --client-secret=... \
       --ledger=payments-ledger
```

### From Source

```bash
go install github.com/formancehq/formance-chart-sync@latest

chart-sync validate charts/payments.yaml
chart-sync list --server-url=... --ledger=...
chart-sync get <version> --server-url=... --ledger=...
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
| `VERSION` | Yes | Schema version prefix |
| `CHART_GLOB` | No | File glob (default: `**/*.chart.yaml`) |
| `DRY_RUN` | No | `true` to validate only |
| `FORCE` | No | `true` to skip Ledger version check |

After a successful push, the tool automatically lists all installed schema versions.

#### `validate <file...>`

Validates one or more chart YAML files against the v4 schema. No network access required.

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
version: "4"
date: "2025-09-16"

# Required: target ledger
ledger:
  name: my-ledger

# Account tree (segments form the address hierarchy)
chart:
  users:
    $userid:
      main:
        .metadata:
          nature: { default: "operating" }
      savings: {}

# Transaction templates with Numscript
transactions:
  DEPOSIT:
    runtime: experimental-interpreter  # required for $variable interpolation
    script: |
      // @feature_flag experimental-account-interpolation
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
    filter: { match: { "metadata[userid]": "$userid" } }
```

### Numscript Runtime

If your transaction scripts use `$variable` interpolation in account addresses (e.g. `@users:$userid:main`), you must:

1. Set `runtime: experimental-interpreter` on the transaction template
2. Enable `--experimental-numscript-interpreter` on the Ledger server

## Version String

The push command builds a version string with provenance metadata:

```
{version}+{repo}.{branch}.{filepath}.{commitSHA7}.{fileHash8}
```

Example: `v1+org-my-repo.main.charts-payments.yaml.abc1234.f3e9a0b1`

This ensures every push is traceable to its source repository, branch, file, and commit.

## Architecture

```
formance-chart-sync/
  main.go                       CLI dispatch and GitHub Actions integration
  internal/
    chart/                      Schema validation, Ledger extraction
    convert/                    YAML-to-JSON conversion (anchors, merge keys)
    push/                       Formance SDK client (push, list, get)
    env/                        Environment variable configuration
    changed/                    GitHub push event file detection
  schema/
    chart_v4.schema.json        JSON Schema for chart validation
  testdata/                     Example charts for testing
  action.yml                    GitHub Action definition
  Dockerfile                    Multi-stage build (scratch runtime)
```

## Development

```bash
# Run tests
go test -race ./...

# Build
go build -o chart-sync .

# Validate testdata
./chart-sync validate testdata/examples/simple.yaml
```

## License

See [LICENSE](LICENSE) for details.
