# Design note: chunt-cfz — Progressive/streamed diagnostics

Status: **measurement complete; recommends RE-SCOPE to synchronous tiered publishing (not the async streaming originally sketched).**

---

## TL;DR

The issue assumed parse would dominate and asked us to *measure before
committing*. We measured. **Parse does not dominate (~36%). The real dominator
is `symbols.Index` (~49% of the full pipeline at 11k lines)** — and it sits on
the critical path of *every* pass, yet only **two nearly-free passes** actually
consume it (`undefined-refs` + `duplicate-defs`, <0.1ms combined after
chunt-4qw's O(1) work). Every slow pass — `wrong-section`, `command-version`,
`protocol-mismatch` — walks the parse tree directly and needs **no** symbol
table.

That inverts the design. The clean win is **synchronous tiered publishing**:
publish the tree-only passes *before* `symbols.Index`, then publish the ref
passes after. ~50% perceived-latency reduction for the bulk of diagnostics on
large configs, with **zero concurrency, zero version-tagging, zero new sync on
`CiscoIOSFeature`** — the single-goroutine assumption holds. The async /
streaming-with-cancellation model the issue worried about (Q4) is **not worth
it**: the synchronous model captures essentially all of the benefit and none of
the risk.

---

## Q1 — Is the perceived-latency win real? (the measurement)

**Method.** New gated harness `perf_pipeline_internal_test.go` (run with
`CHUNT_PERF=1`, `-race` off so it reflects production latency). Synthesizes a
realistic multi-section config from the golden fixtures' noise-free command set,
scaled by `n`, and times each stage as the min of 20 runs. `didChange` today
calls `parser.Parse(content, oldTree)` **without** `tree.Edit` (the
`TextDocumentContentChangeEvent` DTO has no `Range` — see OUT OF SCOPE), so
per-keystroke parse cost ≈ a full re-parse. `parse-incremental(no-Edit)`
confirms the old-tree hint buys nothing without an edit.

**Numbers (Apple Silicon, CGO, no -race):**

| stage (n=1000: 11,045 lines / 263 KB / 1000 diags) | time | share |
| --- | ---: | ---: |
| parse-cold (= per-keystroke) | **27.3 ms** | 18.5% |
| symbols.Index | **72.5 ms** | **49.2%** |
| wrong-section (tree-only) | 23.6 ms | 16.0% |
| command-version (tree-only) | 13.2 ms | 9.0% |
| protocol-mismatch (tree-only) | 10.4 ms | 7.0% |
| version-mismatch (tree-only) | 0.37 ms | — |
| undefined-refs (**needs Index**) | 0.0001 ms | — |
| duplicate-defs (**needs Index**) | 0.10 ms | — |
| syntax (tree-only) | ~0 | — |
| **full pipeline** | **147 ms** | |

Scaling is linear and stable (parse-cold share of parse+passes = 38% / 36% / 36%
at n=50/200/1000; symbols.Index = 1.5× the entire pass sum at every scale).

**Conclusions:**

1. **Parse does not dominate** — refutes the issue's "if parse dominates, close
   as not-worth-it" premise. The issue stays open.
2. **`symbols.Index` is the dominator** and is on the critical path for *all*
   passes, but only feeds two passes that are **negligible** post-chunt-4qw.
3. The slow passes are all **tree-only** — verified by grep: only
   `diagnostics_refs.go` references `f.symbols`; syntax/version/section/protocol
   use `f.keyword` + the tree.

**When it matters.** At n≤200 (~2k lines, ~29 ms full) there is no perceptible
lag — the typist's threshold is ~50–100 ms. Progressive publishing only helps on
**large configs (≳3–4k lines)**, e.g. n=1000 where today the user waits 147 ms.
That is exactly the regime where lag is actually noticeable, so the win is
well-targeted, not theoretical.

---

## The recommended design — synchronous tiered publishing

Today's `DidChange`:

```
parse(27ms) → symbols.Index(72ms) → runDiagnostics(all 7 passes, 47ms) → publish once
```

User sees **all** diagnostics at **147 ms**.

Proposed:

```
parse(27ms) → tree-only passes(47ms) → PUBLISH tier-1
            → symbols.Index(72ms) → ref passes(0.1ms) → PUBLISH tier-2 (accumulated)
```

User sees **~99.97%** of diagnostics (everything except undefined-refs/dup-defs)
at **~74 ms**, the remaining ref diagnostics at 147 ms. **Total work is
unchanged**; the only added cost is one extra `publishDiagnostics` carrying the
accumulated set.

This requires exactly two things:

- **Reorder**: move `f.symbols.Index` to *after* the tree-only passes (and split
  `runDiagnostics` so tree passes run before Index, ref passes after).
- **Publish mid-handler**: the Feature returns diagnostics in tiers (callback or
  slice-of-slices) and the server publishes each. `publishDiagnostics` already
  uses `srv.Notify` from inside the handler; jrpc2 serializes writes, so a
  second `Notify` mid-handler is mechanically trivial.

---

## Answering the remaining open questions (under the synchronous model)

**Q2 — full-replacement semantics.** `publishDiagnostics` is full-replacement.
Tier-2 must therefore carry **tier-1 ∪ tier-2** (accumulate, don't diff). Cost:
2 publishes/change instead of 1, tier-2 carrying the full set. For the large
configs where this matters that is a small, bounded addition to a 147 ms
compute path. Acceptable.

**Q3 — version staleness.** **Not an issue under the synchronous model.** jrpc2
runs `Concurrency=1` (documented at `feature.go:18-20`), so the dispatcher does
not begin version N+1's `didChange` until version N's handler returns. No
in-flight run can be clobbered. **The `version` field is not needed at all** for
the synchronous design — which is exactly why we avoid the whole
cancellation/coalescing design surface. (Adding the field is only required if we
later go async.)

**Q4 — concurrency model.** **Stay synchronous.** Publish between stages on the
single dispatch goroutine. `CiscoIOSFeature` keeps needing **no
synchronization**; the single-goroutine assumption holds unchanged. This
sidesteps every concern the issue raised about workers + cancellation + locking
the feature. **Reject the async/streaming-with-cancellation model** — the
synchronous model captures essentially all the benefit.

**Q5 — typing bursts.** Progressive publishing reduces *perceived* latency, not
*per-keystroke work* — each keystroke still runs the full 147 ms pipeline
(serially, so there is no flooding: at most 2 publishes/keystroke). A debounce
is *complementary* but is its own decision (chunt-4qw deliberately dropped
it). **Ship progressive publishing alone for v1; revisit debounce separately**
only if profiling shows the steady-state keystroke rate is the problem.

**Q6 — client variance.** Real caveat to verify, not a blocker. Neovim's
built-in LSP applies `publishDiagnostics` per-notify (no aggressive coalescing),
so tier-1 should render. VS Code applies per-notify but batches UI on a ~200 ms
frame, which is finer than our ~70 ms inter-tier gap. Helix similar. **Verify on
the target client before claiming the UX win.** If a client coalesces on a timer
> the inter-tier gap, tier-1 gets overwritten by tier-2 before rendering and the
benefit evaporates for that client.

---

## Recommendation

1. **Re-scope chunt-cfz** from "progressive/streamed diagnostics (async)" to
   "**synchronous tiered publishing**" and turn it into concrete deliverables
   (below). Do **not** close it — the measurement refuted the close condition.
2. **Do not** pursue the async/streaming-with-cancellation variant. Defer
   indefinitely; revisit only if incremental sync (DTO `Range` field) ever lands
   and a genuinely async model becomes cheap.
3. Out of scope but worth a future issue: `symbols.Index` is 49% of the pipeline
   and only 0.1 ms of it is consumed by diagnostics — **lazy Index** (defer to
   first completion/hover after a change) would remove it from the `didChange`
   path entirely. Separate, larger change; not cfz.

### Proposed deliverables for the re-scoped chunt-cfz

- **D1** Split `runDiagnostics` into `runTreeDiagnostics` (syntax/version/
  command-version/wrong-section/protocol-mismatch) and `runRefDiagnostics`
  (undefined-refs/dup-defs). Output byte-identical to today (golden + table
  tests guard it).
- **D2** Reorder `DidChange`/`DidOpen`: parse → tree passes → (publish tier-1)
  → `symbols.Index` → ref passes → (publish tier-2, accumulated). Extend the
  `Feature` interface to publish in tiers (callback or `[][]Diagnostic`);
  server publishes each via the existing `publishDiagnostics`.
- **D3** Golden: add a large multi-section fixture asserting the tier-1 set is a
  subset of the final set and the final set equals today's output.
- **D4** Verify on neovim (and VS Code if available) that tier-1 renders before
  tier-2 (Q6).

## Measurement reproducibility

```bash
CHUNT_PERF=1 CGO_ENABLED=1 go test ./internal/features/cisco_ios_jinja2/ \
  -run '^TestPipelineTiming$' -v -count=1
```

---

## Update — implemented & measured (D1–D4)

The synchronous tiered design above landed. D1–D3 are done; D4 (manual client
verification on neovim/VS Code) remains.

**What shipped:**

- **D1** `runDiagnostics` split into `runTreeDiagnostics` (syntax/version/
  command-version/wrong-section/protocol-mismatch) and `runRefDiagnostics`
  (undefined-refs/duplicate-defs) in `diagnostics.go`. `runDiagnostics` kept as a
  one-shot convenience for the perf harness. Output set is identical to before
  (goldens byte-identical; only internal ordering differs, which nothing
  depends on — goldens sort by location, inline tests search by code).
- **D2** `DidOpen`/`DidChange` now take a nilable `publish
  func([]protocol.Diagnostic)`. With a non-nil publish they emit tier 1
  (tree-only) before `symbols.Index`, then the full set after; tier 2 is skipped
  when there are no ref diagnostics (it would be a redundant publish under
  full-replacement semantics). `finishRefDiagnostics` builds the final slice
  fresh (no aliasing of the tier-1 backing array) so an async-queued publish
  cannot observe a later append. The server (`text_document_sync.go`) passes a
  publisher and no longer publishes itself; `cmd/check` and unit tests pass nil.
  The single-goroutine assumption (`feature.go`) holds — zero new sync.
- **D3** `diagnostics_tiered_internal_test.go` asserts the full contract: both
  tiers, tier-1 ⊆ tier-2, tier-1 carries no symbol-table diagnostics, the
  DidOpen/DidChange return value equals the final tier, and a no-op didChange
  agrees with a cold parse.

**Measured win** (`TestTieredLatency`, Apple Silicon, CGO, no `-race`, min of
20 runs) — confirms the ~50% prediction almost exactly:

| scale | tier-1 publish (user sees diags) | full pipeline (DidOpen return) | sooner |
| --- | ---: | ---: | ---: |
| 595 lines | 3.8 ms | 7.3 ms | 48% |
| 2,245 lines | 14.4 ms | 28.6 ms | 50% |
| 11,045 lines | **72.6 ms** | **144.5 ms** | **50%** |

The user sees the bulk of diagnostics at roughly half the latency on large
configs; the remaining time is `symbols.Index` + ref passes, which add new
ref diagnostics only when undefined-refs/duplicate-defs exist.

**D4 (open) — verify client rendering.** A gating manual check before declaring
the UX win shipped: confirm neovim (and VS Code if available) actually renders
the tier-1 publish before tier-2 arrives (~70 ms gap on large configs). See Q6
above — if a client coalesces publishes on a slower timer, the benefit
 evaporates for that client.

**Pre-existing limitation surfaced** (not introduced here, kept out of scope):
`DidChange` parses with the old tree but never calls `tree.Edit` (the LSP
`TextDocumentContentChangeEvent` DTO has no `Range`). For real content changes
the incremental parse can diverge from a cold parse — the tiered tests use a
no-op didChange to stay deterministic. True incremental sync is the separate,
DTO-blocked effort noted in the issue.

**Reproducibility (tiered latency):**

```bash
CHUNT_PERF=1 CGO_ENABLED=1 go test ./internal/features/cisco_ios_jinja2/ \
  -run '^TestTieredLatency$' -v -count=1
```
