What this change does: PR #30 preserves management policies during container recreation and strengthens update authorization and execution exclusion. It also verifies Compose configuration and digest-pinned images. This review assumes one control instance and homelab load.

Initial revision reviewed: `4d98902`. The four findings below were reproduced with failing regression tests, then corrected in this follow-up. A second source review found no remaining blocking findings in the changed flows.

## Must fix — all resolved

1. **P1 — A failed preview leaves the update target locked** (`internal/control/server.go`, `dispatchFailed`).
   - **What this is:** The control distinguishes explicit rejection from uncertain acceptance so an update cannot overlap work that may still be running.
   - **Problem:** The same handling applied to read-only previews. A lost preview response left a queued job forever; the preview agent never sends an asynchronous terminal event to clear it.
   - **Fix:** Fail read-only preview jobs and release their lock on dispatch errors. Keep uncertain mutation locks, and preserve terminal outcomes already received from the agent.
   - **If we skip it:** One preview timeout prevents later updates until manual database reconciliation.

2. **P1 — Compose can apply a file changed during pull** (`internal/agent/updater.go`, `executeCompose`).
   - **What this is:** The deployed service hash checks that Compose uses the original configuration and interpolation inputs.
   - **Problem:** Validation happened before the image download. An operator editing YAML or an env-file during the download could change what the later `up` applied.
   - **Fix:** Repeat full scope, policy and hash validation after pull and before up. Keep the signed sensitive-service policy for that validation.
   - **If we skip it:** A validated image update can still apply different runtime configuration.

3. **P2 — Completed updates remain blocked by inventory refresh** (`internal/agent/server.go`, `execute` and `executeSelfUpdate`).
   - **What this is:** Agent-local exclusion prevents overlapping mutations, including self-update.
   - **Problem:** The exclusion remained active after the terminal result, through event delivery and a read-only refresh that can take 90 seconds. A new update could receive a conflict even though the previous update had finished.
   - **Fix:** Release exclusion after recording the local terminal state and before terminal delivery or refresh. Keep the deferred release as a fallback; it only releases its own job ID.
   - **If we skip it:** The manager reports completion while subsequent operations remain unavailable.

4. **P2 — A pinned image can select another local manifest** (`internal/agent/docker.go`, `matchingDigest`).
   - **What this is:** Docker can report multiple repository digests for an image.
   - **Problem:** Remote lookup respected the pin, but local matching still selected the first digest for that repository. Another digest appearing first produced a false update signal.
   - **Fix:** Select the exact pinned digest from local metadata. If it is absent, report an unknown local digest rather than another manifest.
   - **If we skip it:** Pinned workloads can still show incorrect update availability and undergo needless recreation.

## Verification

- All four regression tests failed against the initial revision and passed after the fixes.
- `go test -race ./...`, `go vet ./...` and `go build ./cmd/...` passed.
- Real Docker integration passed with temporary isolated resources: standalone update and rollback, mount propagation, profiled Compose service isolation, and an injected configuration edit after pull. The edit was rejected before up and the live service remained unchanged.
- The failure-dispatch tests also verify that a completed job is not overwritten or given a misleading unknown-acceptance warning.
- Reviewed callers, job persistence, registration, inventory identity, replica policies, digest selection, terminal event ordering, pruning and existing tests. GitHub's initial CI run was green; no prior review comments or threads were pending.

Verdict: Ship. Local verification passed; merge is gated on successful GitHub Actions for the final revision and a check that the PR head has not moved.

Not checked: production proxy/firewall configuration, live host policy restoration, or actual self-update of a deployed agent. Frontend source is unchanged; its Node 22, build and browser checks run in CI.
