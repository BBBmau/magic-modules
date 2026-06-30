# Runbook: Agent Workflow for Failing Nightly Tests (via TeamCity)

A repeatable process for an agent to triage and fix failing acceptance tests from
the Google Beta nightly suite. Prereq: TeamCity CLI integration verified — see
[`teamcity-integration.md`](./teamcity-integration.md).

> To **choose which** failing test(s) to work on, run the triage front-end first:
> [`nightly-test-triage-workflow.md`](./nightly-test-triage-workflow.md). It scans
> the latest nightly batch and surfaces new regressions + chronic failures (and
> filters out flakes). This runbook covers what to do **once a target is picked**.

## Autonomy (run unattended)
This workflow is long-running and involves many shell/git/`gh`/`teamcity` calls plus
long TeamCity polls. The agent should run **without per-action approval prompts** so a
failing test can be resolved end-to-end without interruption. Start the session with
full permissions:
```bash
copilot --allow-all      # == --allow-all-tools --allow-all-paths --allow-all-urls (alias: --yolo)
# or, mid-session: /allow-all   (or toggle /autopilot)
```
With this set, do **not** stop to ask for confirmation on routine steps (generate,
compile, commit, push, open PR, trigger/poll builds). Only surface a decision when the
fix approach is genuinely ambiguous or a build reveals a real (non-flake) failure.

## Conventions
- Project (Google Beta nightly): `TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS`
- Project (Beta Upstream MM Testing): `TerraformProviders_GoogleCloud_GOOGLE_BETA_MMUPSTREAMTESTS`
- Project (GA Upstream MM Testing): `TerraformProviders_GoogleCloud_GOOGLE_MMUPSTREAMTESTS`
- Branch ref (must use full ref): `refs/heads/nightly-test`
- Source of truth for fixes: `magic-modules` (edit MMv1 YAML / templates, **never** the
  downstream generated provider directly).
- **Validate fixes on TeamCity infra wherever possible** (see Phase 8), not just locally.

---

## Phase 1 — Verify access (once per session)
```bash
teamcity auth status
```
Expect: logged in to `https://hashicorp.teamcity.com`, API compatible.

## Phase 2 — Pick a target service
List failing runs for the latest nightly and choose a scope (start small, e.g. a
service with ~3 failures sharing one root cause):
```bash
teamcity run list \
  --project TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS \
  --branch refs/heads/nightly-test \
  --status failure --since 1d --limit 0 --json
```
Use `statusText` (e.g. `Tests failed: 3 ...`) to find a small, focused failure set.
Prefer a run where all failures are in the **same resource** (likely one root cause).

### ⚠️ Phase 2.5 — Gate on failure rate BEFORE investing (required)
A test that failed in *one* nightly is usually a flake and is **not** worth a fix.
**Only proceed if a candidate test has failed ≥ 50% of its recent runs** (i.e. a
chronic/persistent failure). Check each candidate's history and compute the rate:
```bash
teamcity run tests --test <ExactTestName> \
  --job TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS_GOOGLEBETA_PACKAGE_<SERVICE> \
  --limit 100
# The trailing summary line "TESTS: <N> passed, <M> failed" gives the rate:
#   failure% = M / (N + M)
```
- `failure% ≥ 50%` → persistent failure, **proceed**.
- `failure% < 50%` (e.g. 6/100 = 6%) → likely flaky/transient, **skip it** and pick
  another candidate. (Re-running such a test usually passes — see Phase 8 flake
  handling.)
Prefer the candidates with the **highest** failure rate / longest unbroken failing
streak; those are the real, deterministic bugs worth an agent's time.

## Phase 3 — Pull failure details
```bash
teamcity run tests <runId> --failed          # names only
teamcity run tests <runId> --failed --json    # full stdout/error per test
```
Capture the failure signature (error message + which phase: test step vs
post-test destroy / CheckDestroy).

## Phase 4 — Locate the source in magic-modules
- Resource YAML: `mmv1/products/<service>/<Resource>.yaml`
- Example configs: `mmv1/templates/terraform/examples/*.tf.tmpl` and
  `mmv1/templates/terraform/samples/services/<service>/*.tf.tmpl`
- Custom handlers: `mmv1/templates/terraform/custom_{create,update,delete,check_destroy}/...`

## Phase 5 — Launch a background investigation agent (READ-ONLY)
Use a `general-purpose` background agent. The prompt MUST include:
- Full failure signature + which phase it occurs in.
- Everything already known (config pattern, candidate source files).
- Explicit instruction: **do not edit/create/delete files**; deliverable is a
  root-cause analysis + concrete proposed fix with cited file:line.
- Ask it to (a) read the YAML + custom handlers, (b) determine root cause,
  (c) find precedent fixes elsewhere in the repo, (d) check `git --no-pager log`
  history on the relevant files, (e) state whether it's a real bug vs CI flakiness.

While it runs, do independent work or wait for the completion notification, then
`read_agent` once with `wait: true`.

## Phase 6 — Review & decide fix mode
Confirm the agent cited concrete evidence (file:line) and precedent. Choose:
1. Investigate only (default first pass).
2. Implement fix, stop for review.
3. Implement + validate with acceptance test.

## Phase 7 — Implement the fix
- Edit MMv1 source (YAML / templates), not generated code.
- Mirror precedent patterns the investigation surfaced (e.g. tolerate 404 on
  delete/check_destroy: `if transport_tpg.IsGoogleApiErrorWithCode(err, 404) { return nil }`).
- Sanity-check before opening a PR: generate just the affected product and compile it.
  ```bash
  # from mmv1/ — generate one product into a temp dir
  OUT=$(mktemp -d)
  go run . --output "$OUT" --version beta --product <service> --no-docs
  # copy the generated file(s) into a downstream provider checkout and build
  go build ./google-beta/services/<service>/
  # then revert the downstream copy (magic-modules is the source of truth)
  ```

## Phase 7.5 — Open the upstream PR
Commit **only** the MMv1 source change (not local `.agents/` runbooks). A
`release-note` block in the PR body is **required**.

**Always include a TeamCity test-history link in the PR body** so the reviewer can see
the failure has been chronic (not a one-off flake). Resolve the stable per-test history
URL from the test name:
```bash
# testNameId is stable across builds/branches
TESTNAMEID=$(teamcity api "/app/rest/tests/name:<ExactTestName>" | jq -r .id)
HISTORY_URL="https://hashicorp.teamcity.com/test/${TESTNAMEID}?currentProjectId=TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS&tab=testDetails"
echo "$HISTORY_URL"
```
Put it under a `## Test history` heading along with the Phase 2.5 failure rate, e.g.:
```
## Test history
This test has been failing **<M>/<N>** recent nightly runs on `refs/heads/nightly-test`.
TeamCity test history: <HISTORY_URL>
```
You'll also add a `## Validation build` link to the PR body once the TeamCity run is
triggered in Phase 8 — both links are for Google reviewers, who have TeamCity access.

```bash
git checkout -b <fix-branch>
git add mmv1/...                      # only the fixed YAML/templates
git commit -m "<service>: <summary>"   # do NOT add a Copilot Co-authored-by trailer (breaks CLA/CI)
git push -u origin <fix-branch>        # origin = your fork
gh pr create --repo GoogleCloudPlatform/magic-modules \
  --base main --head <user>:<fix-branch> \
  --title "<service>: <summary>" --body-file <body.md>
```
The PR body must contain a release note, e.g.:
```
```release-note:bug
<service>: fixed a bug where ...
```
```
**Editing an existing PR body:** `gh pr edit` currently fails on this repo with a
`Projects (classic) ... deprecated` GraphQL error (exit 1) and leaves the body
unchanged. Use the REST API instead:
```bash
gh api -X PATCH repos/GoogleCloudPlatform/magic-modules/pulls/<PR#> -F body=@<body.md>
```
**Note the PR number** — modular-magician will name the generated branch
`auto-pr-<PR#>`.

## Phase 8 — Validate (prefer TeamCity infra over local)
Run the fix through the **Upstream MM Testing** pipeline so it executes on the same
infra as nightly, instead of relying solely on a local acceptance run.

1. **Open a PR on upstream `GoogleCloudPlatform/magic-modules`** with the MMv1 change.
2. **modular-magician auto-generates the downstream branch.** Once CI generation
   completes, a branch `auto-pr-<PR#>` is pushed under the **modular-magician**
   namespace of the generated provider repo (e.g.
   `modular-magician/terraform-provider-google-beta`). The corresponding TeamCity
   builds appear under **Upstream MM Testing** on branch `auto-pr-<PR#>`.
3. **Poll/ping until the generated branch exists** — generation takes time. Check
   until it appears, e.g.:
   ```bash
   # poll the modular-magician downstream branch
   gh api repos/modular-magician/terraform-provider-google-beta/branches/auto-pr-<PR#> \
     --silent && echo "branch ready" || echo "not yet"
   # or watch for the TeamCity run to register
   teamcity run list \
     --project TerraformProviders_GoogleCloud_GOOGLE_BETA_MMUPSTREAMTESTS \
     --branch refs/heads/auto-pr-<PR#> --since 1d
   ```
   Loop with a sleep (e.g. 60s) until the branch/run exists. Do **not** assume it is
   ready immediately after opening the PR. **Automate this**: kick off the poll as a
   background process immediately after the PR is created so the agent is notified
   the moment the branch lands, rather than checking manually. Example loop:
   ```bash
   for i in $(seq 1 120); do
     gh api repos/modular-magician/terraform-provider-google-beta/branches/auto-pr-<PR#> \
       --jq .name >/dev/null 2>&1 && { echo BRANCH_READY; break; }
     sleep 60
   done
   ```
4. **Move the run to the top of the queue.** Upstream MM Testing runs otherwise stall
   behind other queued builds. Easiest: start the run already at the top with
   `--top`:
   ```bash
   teamcity run start <PROJECT>_GOOGLEBETA_PACKAGE_<SERVICE> \
     --branch refs/heads/auto-pr-<PR#> --top \
     --comment "Validate <fix> (PR #<PR#>)" --tag pr-<PR#> --json
   ```
   If a run is already queued, move it instead:
   ```bash
   teamcity queue list                      # find the queued run id for your branch
   teamcity queue top <queuedRunId>          # give it highest priority
   ```
   Note: Upstream MM Testing builds are **not** auto-registered for every PR — you
   typically start the specific service job yourself on the `auto-pr-<PR#>` branch.

   ⚠️ **Use the FULL branch ref when triggering.** Pass `--branch refs/heads/auto-pr-<PR#>`,
   **not** the short name `auto-pr-<PR#>` (same convention as the nightly branch
   `refs/heads/nightly-test`). If you pass the short name, TeamCity treats it as an
   unknown logical branch and the build is cancelled within ~1s with:
   `Build stopped: Error: The branch auto-pr-<PR#> does not exist ...`.
   Verify the trigger took by checking the returned `branchName` is
   `refs/heads/auto-pr-<PR#>`, then confirm the run reaches `state: running`
   (e.g. "Resolving deltas …") rather than instantly finishing FAILURE.

   📌 **Capture the build link and add it to the PR body.** The `teamcity run start`
   JSON returns the build `id` and `webUrl`. Add this URL to the PR description (under a
   `## Validation build` heading) so Google reviewers — who have TeamCity access — can
   open the exact run directly instead of hunting for it. Build URL format:
   `https://hashicorp.teamcity.com/buildConfiguration/<JOB_ID>/<runId>`. Update the PR
   body once you have the run id (then again with the final PASS/FAIL result):
   ```bash
   RUNID=$(teamcity run start <JOB_ID> --branch refs/heads/auto-pr-<PR#> --top \
    -P TEST_PREFIX=<ExactTestName> --json | jq -r .id)
   BUILD_URL="https://hashicorp.teamcity.com/buildConfiguration/<JOB_ID>/${RUNID}"
   # append "## Validation build\n<BUILD_URL>" to the PR body, then PATCH it:
   gh api -X PATCH repos/GoogleCloudPlatform/magic-modules/pulls/<PR#> -F body=@<body.md>
   ```
5. **Watch the targeted service job / tests** for the fix:
   ```bash
   teamcity run watch <runId>
   teamcity run tests <runId> --failed
   ```
   ⚠️ **Check the final `status`, not just `state`.** A run can be `state: running`
   with `status: SUCCESS` and still flip to `FAILURE` as tests execute. Only conclude
   success when `state: finished` **and** `status: SUCCESS`. Always re-pull
   `run tests <runId> --failed` at the end. If you background a watcher, it must be
   **detached** (survives the session) or you won't get the completion notification.
6. **Triage remaining failures: is it your change or a pre-existing/flaky test?**
   - Compare the failing test name against the ones you set out to fix. A *different*
     test failing (especially at **apply/Step 1/3** rather than destroy) is usually
     unrelated.
   - Errors like `Provider produced inconsistent result after apply ... Root object
     was present, but now absent` are typically transient create/consistency flakes.
   - **Confirm a flake by re-running just that one test** (≈12 min vs ≈50 min for the
     full suite) by overriding the `TEST_PREFIX` build parameter:
     ```bash
     teamcity run start <PROJECT>_GOOGLEBETA_PACKAGE_<SERVICE> \
       --branch refs/heads/auto-pr-<PR#> --top \
       -P TEST_PREFIX=<ExactTestName> \
       --comment "Re-run flaky <test> (outside PR scope)" --json
     ```
     (Jobs expose `TEST_PREFIX` (default `TestAcc`) and `PACKAGE_PATH`; check via
     `teamcity api "/app/rest/buildTypes/id:<job>/parameters"`.) If it passes on
     re-run with no code change, it's flaky and out of scope for the PR.
7. (Optional / fallback) Local validation: generate the downstream provider and run
   the specific acceptance test(s) directly.
8. Confirm green, then proceed with merge per normal upstream review.

---

## Tracking
Track each experiment in the session DB (`todos` / `todo_deps`): one `investigate`
todo and a dependent `fix` todo per service. Mark `investigate` done before
starting `fix`.

## Lessons learned
- **Gate on failure rate first (≥ 50%).** Before investigating, pull the test's
  history (`run tests --test <name> --job <job> --limit 100`) and compute
  `failed / (passed + failed)`. A test that only failed once (e.g. Monitoring's
  `MetricDescriptor_update` was 6/100 ≈ 6%) is a flake — don't spend an agent on it.
  Prioritize chronic, high-failure-rate tests; those are real deterministic bugs.
- Branch filter requires the **full ref** `refs/heads/nightly-test`; the short
  name returns "No runs found".
- `--json` on `run tests` returns per-test stdout — enough to root-cause without
  opening the web UI.
- Post-test-destroy / CheckDestroy 404s are often **provider cleanup bugs**, not
  CI flakiness (a CheckDestroy GET 404s after the parent resource is destroyed).
- Background read-only investigation agents are effective: give complete context,
  forbid edits, require cited evidence + precedent.
- **Validate on TeamCity infra, not just locally.** Open an upstream MM PR so
  modular-magician generates an `auto-pr-<PR#>` branch; the build runs under
  **Upstream MM Testing**. Generation is not instant — **poll until the branch
  exists** in the modular-magician namespace before expecting a run. Always
  `teamcity queue top <id>` the queued run (or start with `--top`), or it stalls
  behind other builds.
- **GitHub branch existing ≠ buildable... actually it usually is — the real gotcha
  is the branch-ref format.** Trigger Upstream MM builds with the **full ref**
  `--branch refs/heads/auto-pr-<PR#>` (mirrors `refs/heads/nightly-test`). The short
  name `auto-pr-<PR#>` makes TeamCity cancel the build in ~1s with "The branch
  auto-pr-<PR#> does not exist". Confirm the run reaches `state: running` before
  assuming it's executing.
- **Verify the final build `status`, don't infer success from `state: running`.** A
  running build often shows `status: SUCCESS` until a test fails. Conclude success
  only on `state: finished` + `status: SUCCESS`, and always re-check
  `run tests <id> --failed`. Background watchers must be **detached** to notify.
- **Distinguish your regression from unrelated flakes.** A failing test that isn't
  one you targeted — particularly an **apply-time** error like "Root object was
  present, but now absent" on resource create — is usually a pre-existing flake.
- **Re-run a single test cheaply** by overriding the `TEST_PREFIX` build parameter
  (`-P TEST_PREFIX=<ExactTestName>`) instead of re-running the whole service suite.
  A pass on re-run with no code change confirms a flake (out of PR scope).
- **Always embed a TeamCity test-history link in the PR** (Phase 7.5). Resolve the
  stable `testNameId` via `teamcity api /app/rest/tests/name:<TestName>` and build
  `https://hashicorp.teamcity.com/test/<id>?currentProjectId=...&tab=testDetails`.
  This shows the reviewer the failure has been chronic without them hunting for it.
- **Also embed the triggered validation-build link in the PR** (Phase 8). Grab the
  run `id`/`webUrl` from `teamcity run start` and add
  `https://hashicorp.teamcity.com/buildConfiguration/<JOB_ID>/<runId>` under a
  `## Validation build` heading. Google reviewers have TeamCity access and otherwise
  have to manually find the run the agent triggered.
- **`gh pr edit` is broken on this repo** (Projects-classic GraphQL deprecation,
  exits 1, body unchanged). Edit bodies via REST:
  `gh api -X PATCH repos/GoogleCloudPlatform/magic-modules/pulls/<PR#> -F body=@<file>`.
- **Run unattended** with `copilot --allow-all` (or `/allow-all`); don't pause for
  approval on routine generate/compile/commit/push/PR/build steps.
- **When a validation build FAILS, pull the debug-log artifact before re-coding.**
  Upstream MM Testing jobs already run with `env.TF_LOG=DEBUG` and write a
  per-test artifact (`debug-<sha>-<runId>-<TestName>.txt`). List/grab it with
  `teamcity run artifacts <runId>` / `teamcity run download <runId>`. Grep the
  actual **request/response payloads** (e.g. `PUT /…`, the resource JSON, the
  flatten map) to see whether the write was even sent and what the API returned.
  Guessing at root cause without this evidence burned two TeamCity cycles on the
  BigQuery `default_collation` fix.
- **Cheap local API probes settle "is this a provider bug or an API limit?"** With
  active `gcloud` creds you can hit the REST API directly (create/PUT/GET/delete a
  throwaway resource) to learn the true server default and whether a field can be
  cleared. For `default_collation`: a dataset created without it returns the field
  **absent**, and `PUT "defaultCollation":""` clears it with an immediately
  consistent read-back — proving it was a provider bug, not eventual consistency.
- **Verify the fix locally with the real acc test before spending a TeamCity run.**
  Generate, copy the file into the downstream provider, then
  `GOOGLE_PROJECT=hc-terraform-testing GOOGLE_APPLICATION_CREDENTIALS=<sa-key>
  TF_ACC=1 go test ./google-beta/services/<svc>/ -run '^<ExactTest>$' -v`. The
  PreCheck requires a credentials **file** (a short-lived SA key via
  `gcloud iam service-accounts keys create`, deleted afterward) — `GOOGLE_OAUTH_ACCESS_TOKEN`
  alone is rejected. A local PASS de-risks the (slow) TeamCity validation.
- **Clearing an `Optional+Computed` string to `""` is silently dropped by the
  legacy SDK** ("Interpreting the empty string as unset"). If the API's true
  default for the field is empty/absent, the fix is to drop `default_from_api`
  (make it `Optional`-only) so a real diff is produced and `send_empty_value`
  sends the cleared value. Remove any `customCollationDiff`-style `SetNew` helper
  too — `SetNew` only works on computed keys and errors once the field is no
  longer computed.

## Worked example
First run of this workflow: **Firestore** (`google_firestore_field`), nightly run
695470, 3 failing tests. Root cause: custom `CheckDestroy`/delete don't tolerate a
404 when the parent database is already destroyed. Fix: tolerate 404 in
`custom_delete/firestore_field_delete.go.tmpl` and
`custom_check_destroy/firestore_field.go.tmpl`. Opened as upstream PR
**#18127** → generated branch `auto-pr-18127` under Upstream MM Testing (Beta).
Validated on TeamCity run **695660** (`refs/heads/auto-pr-18127`): all 3 target
tests passed (38 passed / 1 failed). The lone failure,
`TestAccFirestoreField_firestoreFieldBasicExample`, was an unrelated apply-time
create flake — confirmed flaky via an isolated re-run (run **695664**,
`-P TEST_PREFIX=TestAccFirestoreField_firestoreFieldBasicExample`, passed). See
session todos `firestore-field-investigate` / `firestore-field-fix` /
`firestore-pr-validate`.

Second run: **Monitoring** (`google_monitoring_metric_descriptor`),
`TestAccMonitoringMetricDescriptor_update`. Root cause: update is a POST-upsert and
the async poll only checked existence, so it read stale data → perma-diff. Fix: a
custom `PollCheckMetricDescriptorUpdate` (new `constants/monitoring_metric_descriptor.go.tmpl`)
wired via `check_response_func_existence` that polls until the API reflects the
configured `description`/`display_name`. Opened as PR **#18131**, validated on
TeamCity run **695670** (3 passed / 0 failed). ⚠️ **Caveat:** this test was only
failing ~6% of the time (6/100), so per **Phase 2.5** it should **not** have been
prioritized — it is the motivating example for the failure-rate gate. See session
todos `mon-md-investigate` / `mon-md-fix` / `mon-md-validate`.
