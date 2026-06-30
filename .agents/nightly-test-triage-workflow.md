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
teamcity run tests --test <TestName> --job <JobId> --limit 40 --json
```
Each entry has `status` (`SUCCESS`/`FAILURE`) and `build.number` /
`build.branchName` / `build.startDate`. Read newest→oldest and compute:
- `streak` = leading consecutive FAILs (how many of the most recent builds failed)
- `rate`   = FAILs / total over the window
- `pre_rate` = FAIL rate of the history *before* the current streak (was it healthy?)

Buckets:
| Bucket | Rule | Action |
| --- | --- | --- |
| **NEW** (regression) | `streak ≥ 2`, previously healthy (`pre_rate ≤ 0.20`, `≥5` prior builds) | **Investigate first** — likely caused by the pinned commit |
| **WATCH** | `streak == 1` on a healthy test (`[1/40] F...........`) | Ambiguous (fresh regression *or* first flake). Re-check next nightly or do an isolated re-run before investing |
| **CHRONIC** | `rate ≥ 0.50`, or `streak ≥ 4` with `rate ≥ 0.30` | Worth fixing — hand to the fix runbook (its ≥50% gate) |
| **FLAKY** | intermittent, low rate, no clean streak | **Skip** |

### High-confidence regression signal: same-service clusters
If **several tests in the same service** flip to NEW in the same build (e.g. 5
DataFusion tests all showing `recent:FFF.`), treat it as a **service-level
regression** — investigate the service together, not test-by-test. Simultaneous
co-failure across a package is the strongest "the merge broke this" signal.

## Phase D — Produce the triage report & hand off
Rank: NEW (by streak length, clustering same-service) → CHRONIC (by rate) →
WATCH → FLAKY. For each NEW / CHRONIC survivor, proceed to
[`nightly-test-agent-workflow.md`](./nightly-test-agent-workflow.md) Phase 4+
(locate source, investigate, fix, validate on Upstream MM Testing).

## Helper script
Scans the current batch, caches raw histories (so you can re-tune the classifier
without re-fetching), and prints a ranked report. Requires `teamcity` on PATH.

```python
#!/usr/bin/env python3
import subprocess, json, os
from collections import Counter

PROJECT = "TerraformProviders_GoogleCloud_GOOGLE_BETA_NIGHTLYTESTS"
NIGHTLY = "refs/heads/nightly-test"
CACHE   = "/tmp/nightly_triage_cache.json"

def tc(a): return subprocess.run(["teamcity"]+a, capture_output=True, text=True).stdout

def failed_runs():
    out = tc(["run","list","--project",PROJECT,"--branch",NIGHTLY,
              "--status","failure","--since","2d","--limit","0","--json"])
    arr = json.loads(out); arr = arr.get("build",arr) if isinstance(arr,dict) else arr
    newest = max(arr, key=lambda b: b.get("startDate",""))
    commit = newest.get("number")
    return commit, [b for b in arr if b.get("number")==commit]

def failing_tests(run_id):
    out = tc(["run","tests",str(run_id),"--failed","--json"])
    try: d = json.loads(out)
    except Exception: return []
    arr = d if isinstance(d,list) else d.get("testOccurrence", d.get("tests",[]))
    return [e.get("name") for e in arr if e.get("name")]

def history(test, job, limit=40):
    out = tc(["run","tests","--test",test,"--job",job,"--limit",str(limit),"--json"])
    try: d = json.loads(out)
    except Exception: return []
    arr = d if isinstance(d,list) else d.get("testOccurrence", d.get("tests",[]))
    return [e.get("status")=="SUCCESS"
            for e in arr if e.get("build",{}).get("branchName")==NIGHTLY]

def classify(h):
    if not h: return "UNKNOWN"
    total=len(h); fails=sum(1 for ok in h if not ok); rate=fails/total
    streak=0
    for ok in h:
        if ok: break
        streak+=1
    pre=h[streak:]
    pre_rate=(sum(1 for ok in pre if not ok)/len(pre)) if pre else 1.0
    if streak>=2 and len(pre)>=5 and pre_rate<=0.20: return "NEW"
    if streak==1 and len(pre)>=5 and pre_rate<=0.20: return "WATCH"
    if rate>=0.50: return "CHRONIC"
    if streak>=4 and rate>=0.30: return "CHRONIC"
    return "FLAKY"

if os.path.exists(CACHE):
    c=json.load(open(CACHE)); commit=c["commit"]; data=c["data"]
else:
    commit, runs = failed_runs(); data=[]; seen=set()
    for b in runs:
        job=b.get("buildTypeId"); rid=b.get("id")
        for t in failing_tests(rid):
            if (t,job) in seen: continue
            seen.add((t,job))
            data.append({"test":t,"job":job,"run":rid,"hist":history(t,job)})
    json.dump({"commit":commit,"data":data}, open(CACHE,"w"))

for d in data: d["cls"]=classify(d["hist"])
order={"NEW":0,"CHRONIC":1,"WATCH":2,"FLAKY":3,"UNKNOWN":4}
def streak(h):
    s=0
    for ok in h:
        if ok: break
        s+=1
    return s
data.sort(key=lambda d:(order[d["cls"]], -streak(d["hist"]),
                        -(sum(1 for ok in d["hist"] if not ok)/len(d["hist"]) if d["hist"] else 0)))
print(f"# Nightly batch commit {commit} — {len(data)} failing (test,job) pairs")
print("SUMMARY:", dict(Counter(d['cls'] for d in data)), "\n")
for d in data:
    if d["cls"] in ("FLAKY","UNKNOWN"): continue   # surface only actionable buckets
    h=d["hist"]; f=sum(1 for ok in h if not ok)
    svc=d["job"].split("PACKAGE_")[-1]
    pat="".join("F" if not ok else "." for ok in h[:12])
    print(f"[{d['cls']:8}] {svc:18} {d['test']}  [{f}/{len(h)}] recent:{pat}")
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
- The scan is ~one history call per failing test (a couple hundred for a bad
  night); cache the raw histories so re-tuning the thresholds costs nothing.
