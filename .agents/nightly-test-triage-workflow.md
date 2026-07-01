# Runbook: Triage of Failing Nightly Tests (via TeamCity)

This is the **analysis / triage** front-end that runs *before*
[`nightly-test-agent-workflow.md`](./nightly-test-agent-workflow.md) (the fix
runbook). Its job: take the **most recent `nightly-test` build**, enumerate
**every** failing test across all service jobs, and classify each one so the
agent only spends effort on real bugs — **new regressions** and **chronic**
failures — and skips low-rate **flakes**.

See [`teamcity-integration.md`](./teamcity-integration.md) for CLI auth, project
IDs, and the base command reference.

## Why this works
The `nightly-test` branch is pinned to the **last commit merged to `main`** of
magic-modules. Every per-service nightly run reports its commit as the build
`number` (a short SHA, e.g. `fceac35`), so the whole nightly batch groups by
that `number`. A test that was green for weeks and starts failing at the current
`number` is almost certainly a **regression introduced by that merge** — exactly
what we want to prioritize. A test that has failed intermittently for a long time
is a flake; a test that has failed for many consecutive builds is chronic.

## Conventions
- Project: `TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS`
- Branch: always the **full ref** `refs/heads/nightly-test` (the short name
  returns "No runs found").
- Each service job id ends in `..._GOOGLEBETA_PACKAGE_<SERVICE>`.

## Phase A — Snapshot the current nightly batch
List failed runs and group by commit `number`; the newest `number` is the
current nightly.

```bash
teamcity run list \
  --project TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS \
  --branch refs/heads/nightly-test \
  --status failure --since 2d --limit 0 --json
```
Each record carries `buildTypeId` (the service job), `id` (runId), and
`number` (commit). Keep only the runs whose `number` equals the most recent
build's `number`.

## Phase B — Enumerate failing tests per service
For each failed run in the batch:
```bash
teamcity run tests <runId> --failed --json   # → list of failing test names
```
> Skip "infra" failures: a run with `statusText: "Tests failed: 0, ..."`, a
> sub-30s duration, or no failing test names is usually a setup/agent error, not
> a code regression.

## Phase C — Classify each failing test from its history
Pull the test's per-build history and **filter to `refs/heads/nightly-test`**
(history mixes in `refs/heads/main` and `auto-pr-*` rows — they must be excluded
for an accurate per-branch signal):
```bash
teamcity run tests --test <TestName> --job <JobId> --limit 60 --json
```
Use a **deep window (≥ 60 builds)** so "has it been failing a long time?" is
answerable — a 40-build window can hide a months-long chronic failure. Each entry
has `status` (`SUCCESS`/`FAILURE`) and `build.number` / `build.branchName` /
`build.startDate`. Read newest→oldest and compute:
- `streak` = leading consecutive FAILs (how many of the most recent builds failed)
- `rate`   = FAILs / total over the window
- `pre_rate` = FAIL rate of the history *before* the current streak (was it healthy?)
- `since`  = `startDate` of the oldest build in the current streak → **the date the
  test started failing** (report this so reviewers see how long it's been broken)

Buckets:
| Bucket | Rule | Action |
| --- | --- | --- |
| **NEW** (regression) | recent streak `2–6`, previously healthy (`pre_rate ≤ 0.20`, `≥5` prior builds) | **Investigate first** — likely caused by the pinned commit |
| **WATCH** | `streak == 1` on a healthy test (`[1/40] F...........`) | Ambiguous (fresh regression *or* first flake). Re-check next nightly or do an isolated re-run before investing |
| **CHRONIC** | `rate ≥ 0.50`, or `streak ≥ 4` with `rate ≥ 0.30` | Worth fixing — hand to the fix runbook (its ≥50% gate) |
| **FLAKY** | intermittent, low rate, no clean streak | **Skip** |

> **Cap the NEW streak.** A test failing the last *15–30* builds straight is no
> longer "new" — it's an old regression that became chronic; the `≤ 6` upper
> bound keeps NEW limited to *recent* breaks and lets long streaks fall through
> to CHRONIC.

### High-confidence regression signal: same-service clusters
If **several tests in the same service** flip to NEW in the same build (e.g. 5
DataFusion tests all showing `recent:FFF.`), treat it as a **service-level
regression** — investigate the service together, not test-by-test. Simultaneous
co-failure across a package is the strongest "the merge broke this" signal.

## Phase C.5 — Assess severity / release impact (read the actual error)
Failure *pattern* (new/chronic) is only half the picture. A test can be 100%
failing yet be a **CI/environment** problem that would **not** affect a released
provider — so always read *why* it failed and gauge whether it is
**release-blocking**. The failing test occurrence carries the full Go test output
(including the real error) in its `details` field:
```bash
teamcity run tests <runId> --failed --json   # each entry has a "details" string
```
Match the error signature to a severity bucket:

| Severity | Signature in `details` | Release impact |
| --- | --- | --- |
| **PRODUCT · permadiff** | `After applying this test step, the non-refresh plan was not empty` | **HIGH** — users get a perpetual diff on the resource |
| **PRODUCT · inconsistent** | `Provider produced inconsistent result after apply`, `unexpected new value`, `Root object was present, but now absent` | **HIGH** — apply corrupts/If mismatches state |
| **PRODUCT · api-reject** | `googleapi: Error 400/409`, `invalid argument`, `is not a valid` | **HIGH** — the provider sends a request the API rejects |
| **PRODUCT · crash** | `panic:`, `nil pointer`, `runtime error` | **HIGH** — provider crash |
| **PRODUCT · cleanup** | `CheckDestroy`, `still exists`, delete 404 | **MED** — lifecycle/delete bug |
| **ENV · perm** | `Error 403`, `Permission '…' denied` | **none** — CI service account missing a role |
| **ENV · quota** | `Error 429`, `quota`, `stockout`, `does not have enough resources` | **none** — project/quota/capacity |
| **ENV · unavail** | `Error 503`, `currently unavailable`, `deadline exceeded` | **none** — transient backend |
| **ENV · service** | `Error code 10/13`, `troubleshooting pages`, `INTERNAL` | **none** — GCP service-side |
| **TEST-only** | `ExpectError`/regex mismatch, assertion on a value the API legitimately changed | **low** — fix the test, not the provider |
| **UNKNOWN** | none matched | **read the `details` manually** before deciding |

Only **PRODUCT** severities are release-blocking. `ENV`/`TEST`/`UNKNOWN` failures
— even at 100% failure rate — are **not** reasons to hold a release; they still
warrant a test/CI fix but at lower priority.

## Phase D — Produce the triage report & hand off
Prioritise by the **pattern × severity** matrix, highest first:
1. **NEW + PRODUCT** — a recent regression that would ship a real bug → fix now
   (cluster same-service ones together).
2. **CHRONIC + PRODUCT** — long-standing real bugs (the fix runbook's ≥50% gate).
3. **WATCH + PRODUCT** — confirm on the next nightly, then treat as NEW.
4. **NEW/CHRONIC + ENV or TEST** — file/fix as CI or test issues, not release
   blockers; skip for provider work.
5. **FLAKY** — skip.

For each PRODUCT survivor, proceed to
[`nightly-test-agent-workflow.md`](./nightly-test-agent-workflow.md) Phase 4+
(locate source, investigate, fix, validate on Upstream MM Testing).

## Helper script
Scans the current batch, caches raw histories (so you can re-tune the classifier
without re-fetching), and prints a ranked report. Requires `teamcity` on PATH.

```python
#!/usr/bin/env python3
import subprocess, json, os, re
from collections import Counter
from datetime import datetime

PROJECT = "TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS"
NIGHTLY = "refs/heads/nightly-test"
CACHE   = "/tmp/nightly_triage_cache.json"

def tc(a): return subprocess.run(["teamcity"]+a, capture_output=True, text=True).stdout

# --- severity from the failure `details` (release impact) ---
SIG = [("PRODUCT-PERMADIFF",   r"non-refresh plan was not empty|plan was not empty|After applying this test step"),
       ("PRODUCT-INCONSISTENT",r"Provider produced inconsistent|unexpected new value|Root object was present, but now absent"),
       ("PRODUCT-CRASH",       r"panic:|nil pointer|runtime error|invalid memory"),
       ("PRODUCT-APIREJECT",   r"googleapi: Error 40[09]|invalid argument|is not a valid|Bad Request"),
       ("PRODUCT-CLEANUP",     r"CheckDestroy|still exists|destroy.*404"),
       ("ENV-PERM",            r"Error 403|Permission '|denied on resource"),
       ("ENV-QUOTA",           r"Error 429|quota|rateLimitExceeded|stockout|does not have enough resources|capacity"),
       ("ENV-UNAVAIL",         r"Error 503|currently unavailable|try again later|deadline exceeded"),
       ("ENV-SERVICE",         r"Error code 1[03],|troubleshooting pages|INTERNAL error"),
       ("TEST-EXPECTERR",      r"ExpectError|didn't match|expected to match")]
def severity(det):
    for label, pat in SIG:
        if re.search(pat, det, re.I): return label
    return "UNKNOWN"
def is_product(sev): return sev.startswith("PRODUCT")

def failed_runs():
    out = tc(["run","list","--project",PROJECT,"--branch",NIGHTLY,
              "--status","failure","--since","2d","--limit","0","--json"])
    arr = json.loads(out); arr = arr.get("build",arr) if isinstance(arr,dict) else arr
    newest = max(arr, key=lambda b: b.get("startDate",""))
    commit = newest.get("number")
    return commit, [b for b in arr if b.get("number")==commit]

def failing_tests(run_id):        # returns (name, details) — details is free from this call
    out = tc(["run","tests",str(run_id),"--failed","--json"])
    try: d = json.loads(out)
    except Exception: return []
    arr = d if isinstance(d,list) else d.get("testOccurrence", d.get("tests",[]))
    return [(e.get("name"), e.get("details","")) for e in arr if e.get("name")]

def history(test, job, limit=60):  # (passed?, startDate) newest-first, nightly-test only
    out = tc(["run","tests","--test",test,"--job",job,"--limit",str(limit),"--json"])
    try: d = json.loads(out)
    except Exception: return []
    arr = d if isinstance(d,list) else d.get("testOccurrence", d.get("tests",[]))
    return [(e.get("status")=="SUCCESS", e.get("build",{}).get("startDate",""))
            for e in arr if e.get("build",{}).get("branchName")==NIGHTLY]

def streak(h):
    s=0
    for ok,_ in h:
        if ok: break
        s+=1
    return s
def classify(h):
    if not h: return "UNKNOWN"
    total=len(h); fails=sum(1 for ok,_ in h if not ok); rate=fails/total; s=streak(h)
    pre=h[s:]; pre_rate=(sum(1 for ok,_ in pre if not ok)/len(pre)) if pre else 1.0
    if 2<=s<=6 and len(pre)>=5 and pre_rate<=0.20: return "NEW"
    if s==1 and len(pre)>=5 and pre_rate<=0.20: return "WATCH"
    if rate>=0.50: return "CHRONIC"
    if s>=4 and rate>=0.30: return "CHRONIC"
    return "FLAKY"
def since(h):                      # date the current fail streak began
    s=streak(h)
    if s==0 or s>len(h): return "?"
    try: return datetime.strptime(h[s-1][1][:8], "%Y%m%d").strftime("%b %d")
    except Exception: return "?"

if os.path.exists(CACHE):
    c=json.load(open(CACHE)); commit=c["commit"]; data=c["data"]
else:
    commit, runs = failed_runs(); data=[]; seen=set()
    for b in runs:
        job=b.get("buildTypeId"); rid=b.get("id")
        for name, det in failing_tests(rid):
            if (name,job) in seen: continue
            seen.add((name,job))
            data.append({"test":name,"job":job,"sev":severity(det),"hist":history(name,job)})
    json.dump({"commit":commit,"data":data}, open(CACHE,"w"))

for d in data: d["cls"]=classify(d["hist"])
# priority: PRODUCT first, then NEW>CHRONIC>WATCH, then streak/rate
patord={"NEW":0,"CHRONIC":1,"WATCH":2,"FLAKY":3,"UNKNOWN":4}
data.sort(key=lambda d:(0 if is_product(d["sev"]) else 1, patord[d["cls"]],
          -streak(d["hist"]),
          -(sum(1 for ok,_ in d["hist"] if not ok)/len(d["hist"]) if d["hist"] else 0)))
print(f"# Nightly batch commit {commit} — {len(data)} failing (test,job) pairs")
prod=Counter(); other=Counter()
for d in data: (prod if is_product(d["sev"]) else other)[d["cls"]]+=1
print("PRODUCT (release-impacting):", dict(prod))
print("ENV/TEST/UNKNOWN           :", dict(other), "\n")
print("=== RELEASE-BLOCKING (PRODUCT severity, NEW or CHRONIC) ===")
for d in data:
    if not is_product(d["sev"]) or d["cls"] not in ("NEW","CHRONIC"): continue
    h=d["hist"]; f=sum(1 for ok,_ in h if not ok); svc=d["job"].split("PACKAGE_")[-1]
    print(f"[{d['cls']:7}] {svc:14} {d['test'][:58]:58} {d['sev']:20} since {since(h)} [{f}/{len(h)}]")
print("\n(ENV/TEST/UNKNOWN failures — even at 100% — are CI/test issues, not release blockers)")
```

Delete the cache (`/tmp/nightly_triage_cache.json`) to force a fresh scan against
the next nightly.

## Gotchas / lessons
- **Filter history to `refs/heads/nightly-test`.** Unfiltered counts mix `main`
  and `auto-pr-*` rows and skew the rate (e.g. a test that's 49F/10P across all
  branches can be 39F/39 — 100% — on nightly-test alone).
- **Group the batch by commit `number`.** Don't mix two nightlies; the newest
  `number` is the current build.
- **`streak == 1` is not yet actionable** (`F...........`). It's the *first*
  failure — equally consistent with a new regression or a first-time flake.
  Confirm on the next nightly or with an isolated re-run
  (`-P TEST_PREFIX=<Test>`) before opening an investigation.
- **Same-service co-failures = regression.** A burst of NEW tests in one package
  at the same commit is far stronger evidence than one isolated NEW test.
- **Read the error, not just the pattern.** A 100%-failing test can be an `ENV`
  (403/quota) issue that a released provider would never hit. The `details` field
  from `run tests <runId> --failed --json` carries the full Go test output — use
  it to separate release-blocking PRODUCT bugs from CI/env noise. `details` is
  **not** present in the per-build history call, only in the `--failed` listing,
  so capture severity during that same enumeration (it's free).
- **`PRODUCT-APIREJECT` (Error 400/409) can over-match.** A 400 is sometimes an
  env/config problem, not a provider bug — confirm by reading `details` before
  treating it as release-blocking.
- The scan is ~one history call per failing test (a couple hundred for a bad
  night); cache the raw histories so re-tuning the thresholds costs nothing.
