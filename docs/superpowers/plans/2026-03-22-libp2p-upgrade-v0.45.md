# go-libp2p v0.38.2 → v0.45.0 Upgrade Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade go-libp2p from v0.38.2 to v0.45.0 (quic-go v0.48.2 → v0.55.0) while staying on Go 1.23, to pick up security fixes, performance improvements, and prepare for future upgrades.

**Architecture:** Incremental version bumps through each breaking change boundary (v0.40, v0.41, v0.42, v0.44, v0.45), verifying compilation and tests pass at each milestone. The biggest migration is go-multiaddr v0.14→v0.15 (Multiaddr interface→concrete type) which touches ~15 files.

**Tech Stack:** Go 1.23, go-libp2p, quic-go, go-multiaddr, go-libp2p-pubsub, go-libp2p-kad-dht

---

## Pre-Upgrade Context

### What breaks at each version boundary

| libp2p | quic-go | Breaking change | Impact on this repo |
|--------|---------|----------------|-------------------|
| v0.40.0 | v0.49.0 | `err == network.ErrReset` → `errors.Is` | **None** — not used in codebase |
| v0.41.0 | v0.50.0 | go-multiaddr v0.15: Multiaddr interface→struct | **HIGH** — MultiaddrStub mock, ~15 files with Multiaddr type usage |
| v0.42.0 | v0.52.0 | ResourceManager gains `VerifySourceAddress` | **LOW** — we use `rcmgr.NewResourceManager()`, not custom impls |
| v0.44.0 | v0.55.0 | Logging: go-log → log/slog | **MEDIUM** — `netMessenger.go` uses `go-log.SetLogLevel` |
| v0.44.0 | v0.55.0 | `DisableObservedAddrManager` removed | **None** — not used |
| v0.45.0 | v0.55.0 | `SetDefaultHandler` for gologshim | **None** — no action needed |

### What does NOT break (despite quic-go v0.53 interface→struct)

The QUIC transport is used via `libp2p.Transport(quic.NewTransport)` — the go-libp2p wrapper abstracts the quic-go API. The quic-go v0.53 interface→struct change is handled internally by go-libp2p v0.43.0+. No direct quic-go type usage in this repo.

### Ecosystem packages that will be pulled up

| Package | Current | Expected after upgrade | Notes |
|---------|---------|----------------------|-------|
| go-multiaddr | v0.14.0 | v0.15.x | Required by libp2p v0.41+ |
| quic-go | v0.48.2 | v0.55.0 | Pulled by libp2p v0.45.0 |
| go-log/v2 | v2.5.1 (indirect) | v2.5.1+ | Becomes direct dependency |
| go-log (v1) | v1.0.5 (direct) | REMOVE | Replaced by go-log/v2 |

---

## File Map

### Files to modify

| File | Reason | Task |
|------|--------|------|
| `go.mod` | Bump libp2p + ecosystem deps | 1, 3 |
| `p2p/mock/multiaddrStub.go` | **DELETE** — Multiaddr is no longer an interface, stub is impossible | 2 |
| `p2p/mock/networkStub.go` | Update Multiaddr type references + ResourceManager | 2 |
| `p2p/libp2p/metrics/connections.go` | Update Multiaddr param types in Notifiee methods | 2 |
| `p2p/libp2p/connectionMonitor/libp2pConnectionMonitorSimple.go` | Update Multiaddr param types in Notifiee methods | 2 |
| `p2p/xdp/connection_notifier.go` | Update Multiaddr param types in Notifiee methods | 2 |
| `p2p/libp2p/discovery/hostWithConnectionManagement.go` | Update `concatenateAddresses` param type | 2 |
| `p2p/libp2p/netMessenger.go` | Replace go-log v1 with go-log/v2 or slog | 3 |
| `p2p/libp2p/netMessenger_test.go` | Update Multiaddr type references in test mocks | 2 |
| `p2p/libp2p/connectionsHandler_test.go` | Update Multiaddr type references in test mocks | 2 |
| `p2p/libp2p/interface.go` | Verify `network.Notifiee` embedding still works | 2 |
| `p2p/libp2p/resourceLimiter/resourceLimiter.go` | Verify rcmgr API compatibility | 4 |
| `p2p/libp2p/resourceLimiter/resourceLimiter_test.go` | Update if rcmgr API changed | 4 |

---

## Tasks

### Task 1: Bump go-libp2p to v0.40.0 (error handling milestone)

**Files:**
- Modify: `go.mod`

This is the safest first step — v0.40.0 introduces `errors.Is` for `network.ErrReset` but we don't use that pattern, so it should be a clean bump.

- [ ] **Step 1: Create upgrade branch**

```bash
git checkout -b feat/libp2p-upgrade
```

- [ ] **Step 2: Bump go-libp2p to v0.40.0**

```bash
go get github.com/libp2p/go-libp2p@v0.40.0
go mod tidy
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```
Expected: Clean build (no errors)

- [ ] **Step 4: Run tests**

```bash
go test ./p2p/... -count=1 -timeout 120s
```
Expected: All pass (except pre-existing peerConnections quic-go panic on Go 1.26)

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: bump go-libp2p to v0.40.0 (error handling changes)"
```

---

### Task 2: Migrate go-multiaddr v0.14 → v0.15 (libp2p v0.41.0)

**Files:**
- Delete: `p2p/mock/multiaddrStub.go`
- Modify: `p2p/mock/networkStub.go`
- Modify: `p2p/libp2p/metrics/connections.go`
- Modify: `p2p/libp2p/connectionMonitor/libp2pConnectionMonitorSimple.go`
- Modify: `p2p/xdp/connection_notifier.go`
- Modify: `p2p/libp2p/discovery/hostWithConnectionManagement.go`
- Modify: `p2p/libp2p/netMessenger_test.go`
- Modify: `p2p/libp2p/connectionsHandler_test.go`
- Modify: `p2p/libp2p/interface.go`

This is the **biggest task**. go-multiaddr v0.15 changes `Multiaddr` from an interface to a concrete `[]Component` type. Key consequences:
- `MultiaddrStub` (which implements the Multiaddr interface) cannot exist — DELETE it
- Function signatures taking `multiaddr.Multiaddr` as parameter remain valid (concrete type works)
- `network.Notifiee` methods like `Listen(network.Network, multiaddr.Multiaddr)` — the signature stays the same but the type is now concrete
- `ma == nil` checks must become `len(ma) == 0` or `ma == nil` (slices can be nil)
- Cannot use `Multiaddr` as map key — use `ma.String()` instead

**Important:** Read the official migration guide at `go-multiaddr/v015-MIGRATION.md` before starting.

- [ ] **Step 1: Bump go-libp2p to v0.41.0**

```bash
go get github.com/libp2p/go-libp2p@v0.41.0
go mod tidy
```

- [ ] **Step 2: Attempt to compile and collect all errors**

```bash
go build ./... 2>&1 | tee /tmp/multiaddr-errors.txt
```
Expected: Multiple compilation errors related to Multiaddr type changes

- [ ] **Step 3: Delete MultiaddrStub**

```bash
rm p2p/mock/multiaddrStub.go
```

The `MultiaddrStub` implements the `Multiaddr` interface which no longer exists. Any tests using it must use real `multiaddr.Multiaddr` values created via `multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")` instead.

- [ ] **Step 4: Fix all Multiaddr type references**

For each file with compilation errors:
1. If the file uses `multiaddr.Multiaddr` as a parameter type → should still compile (concrete type)
2. If the file creates a `MultiaddrStub` → replace with `multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/1234")`
3. If the file uses `Multiaddr` as a map key → use `ma.String()` as key instead
4. If the file checks `ma == nil` → verify this still works (slices can be nil-checked)

Key files to fix:
- `p2p/mock/networkStub.go` — remove any MultiaddrStub references
- `p2p/libp2p/netMessenger_test.go` — replace MultiaddrStub with real multiaddrs
- `p2p/libp2p/connectionsHandler_test.go` — replace MultiaddrStub with real multiaddrs

- [ ] **Step 5: Update Notifiee implementations if signatures changed**

Check if the `network.Notifiee` interface `Listen`/`ListenClose` method signatures changed. These files implement it:
- `p2p/libp2p/metrics/connections.go`
- `p2p/libp2p/connectionMonitor/libp2pConnectionMonitorSimple.go`
- `p2p/xdp/connection_notifier.go`

The parameter type `multiaddr.Multiaddr` should remain the same (now concrete instead of interface), but verify.

- [ ] **Step 6: Verify compilation**

```bash
go build ./...
```
Expected: Clean build

- [ ] **Step 7: Run tests**

```bash
go test ./p2p/... -count=1 -race -timeout 120s
```
Expected: All pass

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "deps: migrate go-multiaddr v0.14→v0.15 + bump go-libp2p to v0.41.0

go-multiaddr v0.15 changes Multiaddr from interface to concrete type.
- Deleted MultiaddrStub (can no longer implement Multiaddr interface)
- Updated test mocks to use real multiaddr values
- Verified Notifiee interface implementations still compile"
```

---

### Task 3: Bump through v0.42→v0.44 (ResourceManager + logging)

**Files:**
- Modify: `go.mod`
- Modify: `p2p/libp2p/netMessenger.go` (go-log migration)
- Modify: `p2p/libp2p/resourceLimiter/resourceLimiter.go` (if API changed)
- Modify: `p2p/libp2p/resourceLimiter/resourceLimiter_test.go` (if API changed)

**Sub-step 3a: Bump to v0.42.0 (ResourceManager VerifySourceAddress)**

- [ ] **Step 1: Bump go-libp2p to v0.42.0**

```bash
go get github.com/libp2p/go-libp2p@v0.42.0
go mod tidy
```

- [ ] **Step 2: Check if ResourceManager API broke**

```bash
go build ./p2p/libp2p/resourceLimiter/...
```

The `VerifySourceAddress` method was added to the `ResourceManager` interface, but we create resource managers via `rcmgr.NewResourceManager()` (which returns a concrete type that already implements the new method). We don't implement the interface ourselves. This should compile cleanly.

If `rcmgr.NewResourceManager` signature or `rcmgr.DefaultLimits`, `rcmgr.NewFixedLimiter`, `rcmgr.WithLimitPerSubnet`, `rcmgr.ConnLimitPerSubnet`, or `limits.Scale()` changed — fix accordingly.

- [ ] **Step 3: Verify full compilation + tests**

```bash
go build ./... && go test ./p2p/libp2p/resourceLimiter/... -count=1 -race
```
Expected: Clean

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum p2p/libp2p/resourceLimiter/
git commit -m "deps: bump go-libp2p to v0.42.0 (ResourceManager VerifySourceAddress)"
```

**Sub-step 3b: Bump to v0.43.0 (absorbs quic-go v0.53→v0.54)**

- [ ] **Step 5: Bump go-libp2p to v0.43.0**

```bash
go get github.com/libp2p/go-libp2p@v0.43.0
go mod tidy
```

- [ ] **Step 6: Verify compilation + tests**

```bash
go build ./... && go test ./p2p/... -count=1 -timeout 120s
```
Expected: Clean — quic-go v0.53 interface→struct changes are internal to libp2p

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: bump go-libp2p to v0.43.0 (quic-go v0.54.0)"
```

**Sub-step 3c: Bump to v0.44.0 (logging migration)**

- [ ] **Step 8: Bump go-libp2p to v0.44.0**

```bash
go get github.com/libp2p/go-libp2p@v0.44.0
go mod tidy
```

- [ ] **Step 9: Fix go-log import in netMessenger.go**

The file currently uses:
```go
import logging "github.com/ipfs/go-log"
// ...
_ = logging.SetLogLevel("*", "PANIC")
_ = logging.SetLogLevel(external, "DEBUG")
```

Replace with go-log/v2:
```go
import logging "github.com/ipfs/go-log/v2"
// API should be the same: logging.SetLogLevel() exists in v2
```

If the API differs, check `go-log/v2` docs. The `SetLogLevel` function exists in both v1 and v2.

Also remove `github.com/ipfs/go-log` (v1) from `go.mod` direct dependencies if it's no longer needed.

- [ ] **Step 10: Verify compilation + tests**

```bash
go build ./... && go test ./p2p/... -count=1 -timeout 120s
```

- [ ] **Step 11: Commit**

```bash
git add go.mod go.sum p2p/libp2p/netMessenger.go
git commit -m "deps: bump go-libp2p to v0.44.0, migrate go-log v1→v2"
```

---

### Task 4: Final bump to v0.45.0 + ecosystem packages

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Bump go-libp2p to v0.45.0**

```bash
go get github.com/libp2p/go-libp2p@v0.45.0
go mod tidy
```

- [ ] **Step 2: Optionally bump ecosystem packages**

These are safe incremental bumps that don't require code changes:
```bash
# pubsub — v0.13.0 → v0.14.3 (batch publishing, thread-safety fixes)
go get github.com/libp2p/go-libp2p-pubsub@v0.14.3

# kad-dht — v0.29.0 → v0.29.1 (critical context cancellation fix)
# NOTE: v0.29.1 requires libp2p v0.40.0 which we now have
go get github.com/libp2p/go-libp2p-kad-dht@v0.29.1

go mod tidy
```

Do NOT bump pubsub to v0.15.0 (requires Go 1.24) or kad-dht beyond v0.29.1 (requires libp2p v0.41.1+ which we have, but higher versions need Go 1.24+).

- [ ] **Step 3: Verify full compilation**

```bash
go build ./...
```

- [ ] **Step 4: Run full test suite**

```bash
go test ./... -count=1 -race -timeout 180s
```
Expected: All pass except pre-existing peerConnections panic on Go 1.26

- [ ] **Step 5: Run tests with Go 1.23 if possible**

If you have Go 1.23 available (via `go1.23` or goenv), run the full suite with it to verify production compatibility:
```bash
go1.23 test ./... -count=1 -timeout 180s
```

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: bump go-libp2p to v0.45.0, pubsub to v0.14.3, kad-dht to v0.29.1

Final target versions:
- go-libp2p: v0.38.2 → v0.45.0
- quic-go: v0.48.2 → v0.55.0 (indirect)
- go-multiaddr: v0.14.0 → v0.15.x
- go-libp2p-pubsub: v0.13.0 → v0.14.3
- go-libp2p-kad-dht: v0.29.0 → v0.29.1
- go-log: v1.0.5 → removed (using v2)"
```

---

### Task 5: Verification + cleanup

- [ ] **Step 1: Check for any remaining go-log v1 references**

```bash
grep -r "github.com/ipfs/go-log\"" --include="*.go" .
```
Expected: No matches (all should be go-log/v2)

- [ ] **Step 2: Check for any stale multiaddr mock references**

```bash
grep -r "MultiaddrStub" --include="*.go" .
```
Expected: No matches

- [ ] **Step 3: Verify go.mod is clean**

```bash
go mod verify
go mod tidy
git diff go.mod go.sum
```
Expected: No changes (already tidy)

- [ ] **Step 4: Run linter if configured**

```bash
make lint 2>/dev/null || echo "No lint target"
```

- [ ] **Step 5: Final commit if cleanup needed**

---

## Risk Assessment

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| go-multiaddr v0.15 breaks more than expected | Medium | Compile at each step, fix iteratively |
| rcmgr API changed beyond VerifySourceAddress | Low | We use high-level factory functions |
| Indirect deps conflict | Low | `go mod tidy` resolves |
| kad-dht/pubsub incompatibility | Low | Only bumping to compatible versions |
| quic-go v0.53 leaks through libp2p wrapper | Very Low | No direct quic-go usage |

## Rollback Plan

Each task produces a separate commit. If any step fails catastrophically:
```bash
git revert <failing-commit>
```
Or to rollback everything:
```bash
git reset --hard feat/xdp  # back to XDP work
```
