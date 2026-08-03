---
name: validate-list-resource
description: "Post-codegen QA gate for the add-list-resource workflow. Reads the coder agent's output, validates that the generated list-query tests pass, opens a PR against BBBmau/magic-modules on success, and writes a draft oracle entry to the knowledge base. Invoke after a coder agent has set generate_list_resource: true and generated the downstream provider."
---

# `validate-list-resource`

> **Note to AI Agents:** You MUST read the YAML frontmatter above first. Only read the rest of this
> file if the `description` matches your current roadblock or required task.

This skill is the **validation and ship gate** that runs after `add-list-resource-workflow` (or an
equivalent coder agent) has made its changes. It does four things in sequence:

1. Reads and verifies the coder agent's output
2. Runs the generated list-query tests in an autonomous retry loop until all remaining resources pass
3. Opens a PR against `BBBmau/magic-modules` — including the reasoning for any dropped resources
4. Appends a draft oracle entry to the knowledge base for future agents

**No user interaction is required between Steps 1–4.** The only time a human sees output is when
the PR is ready for review. All failure triage, resource dropping, and re-generation happens
autonomously inside the loop described in Step 2.

---

## Prerequisites

* You are in the `magic-modules` root directory.
* The coder agent has already flipped `generate_list_resource: true` on each target resource YAML and
  run `make provider` to generate the downstream code.
* `$GOPATH` is set and `terraform-provider-google` is present at
  `$GOPATH/src/github.com/hashicorp/terraform-provider-google`.
* The `BBBmau` remote is registered (`git remote get-url BBBmau`). Add it if missing:
  ```bash
  git remote add BBBmau https://github.com/BBBmau/magic-modules.git
  ```
* `gh` CLI is authenticated (`gh auth status`).
* GCP credentials are available for live tests (`GOOGLE_PROJECT`, `GOOGLE_REGION`, `GOOGLE_ZONE`,
  `GOOGLE_CREDENTIALS` or ADC).

---

## Execution Steps

### 1. Read and Verify the Coder Agent's Output

Collect three pieces of information that the coder agent must have produced:

| Required input | Where to find it |
|---|---|
| **Product name** | Passed by the calling workflow or user (e.g. `compute`) |
| **List of opted-in resources** | From the coder agent's summary; verify with `git diff --name-only` |
| **Test output file path** | Default: `/tmp/list_query_test.out`; confirm it exists |

```bash
PRODUCT=<product>   # fill in from context

# Verify the YAML changes exist and look correct
git diff --name-only HEAD | grep "mmv1/products/$PRODUCT/"

# Verify downstream files were generated per opted-in resource
ls "$GOPATH/src/github.com/hashicorp/terraform-provider-google/google/services/$PRODUCT/" \
  | grep '^list_'

# Verify test output is present
ls -lh /tmp/list_query_test.out 2>/dev/null || echo "MISSING – coder agent did not leave test output"
```

If the test output file is missing, run the tests now (Step 2). If the YAML diffs or downstream
files are absent, **stop** and report that the coder agent's output is incomplete — do not proceed.

### 2. Run (or Re-run) the Generated List-Query Tests

Even if `/tmp/list_query_test.out` already exists, re-run the tests from the `magic-modules` root to
get a fresh, reproducible result tied to the current working tree.

```bash
cd "$GOPATH/src/github.com/hashicorp/terraform-provider-google"

TF_ACC=1 go test -v -timeout 120m \
  "./google/services/$PRODUCT/..." \
  -run 'ListQuery_generated$' \
  2>&1 | tee /tmp/list_query_test.out
```

#### Parse results and classify resources

After each test run, parse the output and update the three running lists:

- **`PASSING`** — resources whose test passed
- **`FAILING`** — resources whose test failed and need triage
- **`DROPPED`** — resources removed from the candidate set with a recorded reason

```bash
# After each test run, parse results
FAILURES=$(grep '^--- FAIL:' /tmp/list_query_test.out | awk '{print $3}')
PASSES=$(grep  '^--- PASS:' /tmp/list_query_test.out | awk '{print $3}')

echo "PASS: $(echo "$PASSES" | grep -c .)"
echo "FAIL: $(echo "$FAILURES" | grep -c .)"
```

**Per-failure triage (this agent's responsibility — no user input):**

#### Step 2a — Classify the test type first

Before diagnosing *why* a test failed, determine *what kind* of test it is. Not all failing tests
are generated tests — a resource may also need a handwritten acceptance test that does not yet exist.

```bash
# Check whether the failing test name ends in _generated
# Generated:   TestAcc<ResourceCamelCase>ListQuery_generated
# Handwritten: TestAcc<ResourceCamelCase>_basic / _update / etc.
echo "$FAILURES" | grep -v '_generated$'   # these are handwritten test failures
echo "$FAILURES" | grep    '_generated$'   # these are generated test failures
```

| Test name suffix | Classification | Triage path |
|---|---|---|
| Ends in `ListQuery_generated` | Auto-generated by the list-resource generator | Use the **generated test triage table** below |
| Does **not** end in `_generated` | Handwritten test — may be missing or broken | Use the **handwritten test path** below |

##### Generated test failures — triage table

For each failing `_generated` test, extract the resource name and diagnose the failure from the log:

| Failure symptom | Diagnosis | Action |
|---|---|---|
| `list URL has unsupported scope param` / non-`project`/`region`/`zone`/`location` path param in error | Ineligible — scope param not auto-injected | Drop resource; record reason `"list URL requires unsupported scope param <param>"` |
| `exclude_identity_generation` or `exclude_read` in YAML | Hard generator constraint | Drop resource; record reason `"exclude_identity_generation or exclude_read set"` |
| First example has `exclude_test: true` | Test config unavailable | Drop resource; record reason `"first example has exclude_test"` |
| API returns 403 / permission denied | Missing IAM / credential scope | Drop resource; record reason `"API returned 403 — resource may need elevated permissions or is not available in test project"` |
| API returns 404 / resource type not found | Resource not available in test project/region | Drop resource; record reason `"API returned 404 — resource type not available in test environment"` |
| Compile error in generated `list_*.go` | Generator produced invalid Go | Drop resource; record reason `"generator produced invalid Go — escalate to generator fix"` |
| Transient / timeout / network error | Likely flake | **Retry once** in the next iteration before dropping; record reason `"transient failure after retry"` if it fails again |
| Any other failure with no clear cause | Unresolvable in this run | Drop resource; record reason `"unknown failure — see test log"` |

##### Handwritten test failures — separate path

A non-`_generated` test failure means the resource has a handwritten acceptance test that is either
missing or broken. **Do not drop the resource for this.** The correct action is to hand off to the
coder agent to write or fix the handwritten test:

> "The following resources have handwritten test failures (non-generated). Write or fix the
> acceptance test(s) for each resource in `mmv1/third_party/terraform/services/<product>/` and
> return when the tests pass: `<resource list with failing test names>`."

Wait for the coder agent to return with passing handwritten tests before re-running Step 2. These
resources remain in `FAILING` (not `DROPPED`) until the handwritten tests pass.

If the coder agent determines a handwritten test cannot be written (e.g. the resource requires
manual setup that cannot be automated), then and only then move the resource to `DROPPED` with
reason `"handwritten test required but cannot be automated"`.

#### Hand off to the coder agent when re-generation is needed

This agent **does not run `make provider` itself.** After triage, if any resources were added to
`DROPPED`, pass control back to the `add-list-resource-workflow` skill with an explicit instruction:

> "Remove `generate_list_resource: true` from the following resources and re-run `make provider`
> for product `<PRODUCT>`: `<dropped resource list>`. Return the new test output and the updated
> list of opted-in resources when done."

Wait for the coder agent to return before continuing. When it returns, go back to the top of
Step 2 and re-run the tests against the freshly generated downstream.

#### Loop exit conditions

| State | Action |
|---|---|
| `FAILING` is empty and `PASSING >= 1` | ✅ No handoff needed — proceed to Step 3 |
| `FAILING` is empty and `PASSING == 0` | All resources were dropped — do not open a PR; write the oracle entry with all drop reasons and stop |
| This iteration dropped zero new resources (stuck) | Treat all remaining `FAILING` as `DROPPED` with reason `"no progress after retry"`; proceed to Step 3 with whatever is in `PASSING` (or abort if `PASSING` is also empty) |

Do **not** patch the generator, add `ImportStateVerifyIgnore`, or disable test logic to make a
failure disappear. The correct resolution is always to drop the resource and record why.

### 3. Open the PR Against `BBBmau/magic-modules`

Stage only the YAML edits in `magic-modules` — downstream provider files are throwaway artifacts.

```bash
cd <magic-modules-root>

BRANCH="add-${PRODUCT}-list-resources"

# Ensure we are on the right branch (coder agent may have already created it)
git checkout "$BRANCH" 2>/dev/null || {
  git fetch upstream main
  git checkout -b "$BRANCH" upstream/main
}

# Stage only the product YAML directory
git add "mmv1/products/$PRODUCT/"
git status  # review before committing

# Commit (skip if coder agent already committed)
git diff --cached --quiet || \
  git commit -m "$PRODUCT: add list resources for $(git diff --cached --name-only | wc -l | tr -d ' ') resources"

# Push to the fork
git push BBBmau "$BRANCH"
```

#### Build the PR body

Write the PR description to `/tmp/pr_body.md`. The body must include:
- The opted-in (passing) resources with release-note blocks
- A **Dropped Resources** section explaining why each resource was excluded — this is the primary
  record of eligibility decisions for future agents and reviewers
- The trimmed test output

```bash
PASS_LINES=$(grep '^--- PASS:' /tmp/list_query_test.out | sed 's/^--- PASS: //')
OK_LINE=$(grep '^ok ' /tmp/list_query_test.out || true)

# PASSING_RESOURCES and DROPPED_MAP are populated by the retry loop above.
# Write them out as variables before building the body.

cat > /tmp/pr_body.md <<EOF
Adds list-resource generation for the following $PRODUCT resources:

$(echo "$PASSING_RESOURCES" | sed "s/^/- \`google_${PRODUCT}_/; s/$/\`/")

$(echo "$PASSING_RESOURCES" | sed "s/.*/\`\`\`release-note:new-list-resource\ngoogle_${PRODUCT}_&\n\`\`\`\n/")

## Dropped Resources

The following resources were evaluated and excluded from this PR. The reasons are recorded here
for future agents and for the oracle entry in \`.agents/knowledge/list-resource/list-resource-patterns.md\`.

$(echo "$DROPPED_RESOURCES_WITH_REASONS" | sed 's/^/- /')

<details><summary>Local test output</summary>

\`\`\`
$PASS_LINES
$OK_LINE
\`\`\`

</details>
EOF
```

#### Create the PR

```bash
gh pr create \
  --repo BBBmau/magic-modules \
  --base main \
  --head "BBBmau:$BRANCH" \
  --title "$PRODUCT: add list resources" \
  --body-file /tmp/pr_body.md
```

Capture the PR URL printed by `gh pr create` and report it to the user.

### 4. Write the Draft Oracle Entry

After a successful PR (or after all resources are dropped), commit the oracle entry to a **dedicated
oracle branch** on `BBBmau/magic-modules`. This branch is separate from the product PR branch so
that oracle entries accumulate over time independently of individual product runs and can be reviewed
as a single ongoing PR.

#### 4a. Ensure the oracle branch exists

```bash
ORACLE_BRANCH="oracle/list-resource-patterns"

# Check if the branch already exists on the remote
if git ls-remote --exit-code BBBmau "$ORACLE_BRANCH" > /dev/null 2>&1; then
  # Branch exists — fetch and check it out
  git fetch BBBmau "$ORACLE_BRANCH"
  git checkout -B "$ORACLE_BRANCH" "BBBmau/$ORACLE_BRANCH"
else
  # First run — create the branch from upstream/main
  git fetch upstream main
  git checkout -b "$ORACLE_BRANCH" upstream/main
fi
```

#### 4b. Write the run entry

The file `.agents/knowledge/list-resource/list-resource-patterns.md` is **agent-created metadata**
— it does not exist until the first run produces it. Check which case applies:

```bash
ls .agents/knowledge/list-resource/list-resource-patterns.md 2>/dev/null \
  && echo "EXISTS — append" || echo "MISSING — create"
```

**If the file does not exist (first run):** create it with the full frontmatter header followed by
the first `## Run:` section:

```markdown
---
name: list-resource-patterns
description: Observed patterns, gotchas, and eligibility findings from completed add-list-resource runs.
topics: [list-resource]
task_types: [add-list-resource]
source: authored — agent-generated from validate-list-resource skill
status: draft
last_verified: <YYYY-MM-DD>
---

# List-Resource Patterns

## Run: <product> — <YYYY-MM-DD>

**PR:** <PR URL>  (or "no PR opened — all resources dropped" if applicable)

**Opted-in resources:** <comma-separated list, or "none">

**Dropped resources (with reason):**
- `<resource>` — <reason, e.g. "list URL has scope param `disk`">

**Observations:**
- <any non-obvious finding, e.g. "zone-scoped resources require GOOGLE_ZONE to be set in env">
- <YAML placement: `generate_list_resource: true` placed adjacent to `immutable:` or `has_self_link:`>
- <test format: `TestAcc<ResourceName>ListQuery_generated` — one test per resource>
```

**If the file already exists (subsequent run):** append only a new `## Run:` section (the four
`**...**` fields) below the last existing entry. Do not touch the frontmatter or any earlier sections.
Also update `last_verified` in the frontmatter to today's date.

#### 4c. Commit and push to the oracle branch

```bash
git add .agents/knowledge/list-resource/list-resource-patterns.md
git commit -m "knowledge: list-resource-patterns — $PRODUCT run $(date +%Y-%m-%d)"
git push BBBmau "$ORACLE_BRANCH"
```

Return to the product branch after the oracle commit so the workspace is clean:

```bash
git checkout "$BRANCH" 2>/dev/null || git checkout main
```

---

## Guardrails

* **No user interaction between Steps 1–4.** All triage, dropping, and re-generation is autonomous.
  The user only sees the finished product PR URL.
* **Never open a PR with failing tests.** The retry loop must resolve every failure by dropping the
  resource before a PR is created.
* **Never commit downstream provider files** (`*.go` generated under `google/services/`) to the
  magic-modules branch.
* **Never edit the generator** (`mmv1/api/`, `mmv1/templates/terraform/list_resource*`). If the
  generator misbehaves, drop the affected resource and record the reason.
* **Oracle entries are `status: draft`** until a human reviewer promotes them. Do not mark them
  `certified` yourself.
* **The oracle branch (`oracle/list-resource-patterns`) is append-only.** Never force-push or
  rebase it after the first push — each commit is a permanent record of one run.
* **One product per PR.** Do not bundle multiple products in a single branch.
* **If all resources are dropped,** do not open a product PR. Still commit the oracle entry with
  all drop reasons — the oracle branch is always updated regardless of whether a product PR is opened.
