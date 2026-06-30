# TeamCity CLI Integration — Agent Access to Nightly Tests

## Goal
Enable the agent to access TeamCity builds and test results so it can (eventually)
work on failing nightly tests in the background.

## What We Verified

### 1. CLI is installed and usable by the agent
- Binary: `/opt/homebrew/bin/teamcity` (TeamCity CLI **v1.2.1**)
- The agent can invoke it directly via shell.

### 2. Authentication works
- Logged in to `https://hashicorp.teamcity.com` as **bbbmau** (Mauricio Alvarez Leon)
- Credentials stored in the system keyring
- Server: TeamCity 2025.11 — API reported compatible
- Verified with: `teamcity auth status`

### 3. Target project located
- **Project:** `TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS`
- Path: Terraform Providers / Google Cloud / **Google Beta** / **Nightly Tests**
- Connected to the `hashicorp/terraform-provider-google-beta` repository
- One job per service package (e.g. Datalineage, Compute, BigQuery, ...), all "Active"

### 4. Listing failed builds on the nightly-test branch
The nightly schedule runs against branch `refs/heads/nightly-test`.

```bash
teamcity run list \
  --project TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS \
  --branch refs/heads/nightly-test \
  --status failure --since 1d --limit 0
```

- Returns all failing service jobs for the latest nightly (60+ failing packages observed).
- **Gotcha:** the branch filter must use the full ref `refs/heads/nightly-test`.
  The short name `nightly-test` returns "No runs found".

### 5. Drilling into individual test failures
```bash
# Failed tests within a specific run (e.g. Datalineage run 695613)
teamcity run tests <runId> --failed
```

Example output:
```
✗ TestAccDataLineageConfig_dataLineageConfigFolderExample
TESTS: 1 failed
```

## Useful Command Reference
| Purpose | Command |
| --- | --- |
| Auth status | `teamcity auth status` |
| List projects | `teamcity project list` |
| View a project | `teamcity project view <projectId>` |
| List jobs in project | `teamcity job list --project <projectId>` |
| List runs (filtered) | `teamcity run list --project <id> --branch refs/heads/nightly-test --status failure` |
| Failed tests in a run | `teamcity run tests <runId> --failed` |
| Follow one test's history | `teamcity run tests --test <name>` |
| JSON output | append `--json` |
| Plain/scriptable output | append `--plain --no-header` |

## Key IDs
- Google Beta Nightly project: `TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS`
- Nightly branch ref: `refs/heads/nightly-test`

## Status
✅ TeamCity successfully integrated — the agent has confirmed read access to
nightly-test builds and per-test failure details for Google Beta.

## Related workflows
- **Triage / analysis** of the latest nightly batch (which tests to work on):
  [`nightly-test-triage-workflow.md`](./nightly-test-triage-workflow.md)
- **Fix** runbook (investigate → fix in MMv1 → validate on Upstream MM Testing):
  [`nightly-test-agent-workflow.md`](./nightly-test-agent-workflow.md)
