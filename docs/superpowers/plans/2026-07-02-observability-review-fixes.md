# Observability Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make memcache observation report protocol failures correctly, emit current OpenTelemetry database conventions, and continuously verify both Go modules.

**Architecture:** Keep the backend-neutral observer in the core package and derive observation metadata at the `Client` execution boundary. Keep OpenTelemetry-specific endpoint parsing and semantic-convention mapping inside the separate adapter module. Treat the nested module as an independent CI unit while preserving the existing root checks.

**Tech Stack:** Go 1.25, memcache meta protocol, OpenTelemetry Go 1.44, semantic conventions v1.41.0, DevBuddy, GitHub Actions, golangci-lint.

---

### Task 1: Propagate protocol errors and response status

**Files:**
- Modify: `observer.go`
- Modify: `observer_test.go`
- Modify: `client.go`

- [ ] **Step 1: Write failing helper and single-operation tests**

Extend `TestResultOf` coverage and add tests that exercise real client execution. A successful response must record its status, while a protocol response error must reach `OpResult.Err` even though `Client.Execute` returns it through `Response.Error` rather than the Go error result.

```go
func TestClient_Observer_ProtocolError(t *testing.T) {
	obs := &recordingObserver{}
	client := NewClient(StaticServers("localhost:11211"), Config{
		Dialer:   &mockDialer{conn: testutils.NewConnectionMock("SERVER_ERROR unavailable\r\n")},
		Observer: obs,
	})
	t.Cleanup(client.Close)

	_, err := client.Get(context.Background(), "k")
	require.Error(t, err)
	require.Len(t, obs.results, 1)
	require.ErrorIs(t, obs.results[0].Err, err)
}
```

Also assert `Status: "VA"`, `"EN"`, and `"HD"` in the existing table-driven success tests.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./... -run 'Test(Client_Observer|ResultOf)'`

Expected: FAIL because `OpResult` has no status and protocol responses currently produce `Err == nil` for the observer.

- [ ] **Step 3: Add response metadata helpers and wire single execution**

Add a status field and helpers in `observer.go`:

```go
type OpResult struct {
	Result Result
	Status string
	Err    error
}

func observedError(resp *meta.Response, err error) error {
	if err != nil {
		return err
	}
	if resp != nil {
		return resp.Error
	}
	return nil
}

func responseStatus(resp *meta.Response) string {
	if resp == nil {
		return ""
	}
	return string(resp.Status)
}
```

Use both helpers from the deferred `ActiveOp.End` call in `Client.Execute` without changing the method's public return behavior.

- [ ] **Step 4: Write a failing batch protocol-error test**

Exercise `ExecuteBatch` with a response sequence containing a `SERVER_ERROR` followed by the no-op marker. Assert the client retains its response-based API and the batch observer receives the protocol error.

```go
func TestClient_Observer_BatchProtocolError(t *testing.T) {
	obs := &recordingObserver{}
	client := NewClient(StaticServers("localhost:11211"), Config{
		Dialer: &mockDialer{conn: testutils.NewConnectionMock(
			"SERVER_ERROR unavailable\r\n",
			"MN\r\n",
		)},
		Observer: obs,
	})
	t.Cleanup(client.Close)

	responses, err := client.ExecuteBatch(context.Background(), []*meta.Request{
		meta.NewRequest(meta.CmdGet, "k", nil),
	})
	require.NoError(t, err)
	require.Error(t, responses[0].Error)
	require.Error(t, obs.results[0].Err)
}
```

- [ ] **Step 5: Run the batch test and verify RED**

Run: `go test . -run TestClient_Observer_BatchProtocolError`

Expected: FAIL because batch completion currently forwards only the batch-level Go error.

- [ ] **Step 6: Defer batch completion and derive the observed error**

Add a helper that returns the batch-level error first, otherwise the first non-nil response error. Store the observed error in the goroutine and defer `End` once. If response count validation fails, assign that `OpError` before returning.

```go
func observedBatchError(responses []*meta.Response, err error) error {
	if err != nil {
		return err
	}
	for _, resp := range responses {
		if resp != nil && resp.Error != nil {
			return resp.Error
		}
	}
	return nil
}
```

- [ ] **Step 7: Verify core observer tests and commit**

Run: `go test ./... -run 'Test(Client_Observer|ResultOf)'`

Expected: PASS.

Commit:

```bash
git add observer.go observer_test.go client.go
git commit -m "Report protocol errors to observers"
```

### Task 2: Adopt current OpenTelemetry database conventions

**Files:**
- Modify: `otelmemcache/otelmemcache.go`
- Modify: `otelmemcache/otelmemcache_test.go`

- [ ] **Step 1: Rewrite span expectations before production code**

Update `TestObserver_EmitsSpan` to require:

```go
require.Equal(t, "get 10.0.0.1:11211", span.Name())
require.Equal(t, "memcached", attrs["db.system.name"].AsString())
require.Equal(t, "get", attrs["db.operation.name"].AsString())
require.Equal(t, "10.0.0.1", attrs["server.address"].AsString())
require.Equal(t, int64(11211), attrs["server.port"].AsInt64())
require.Equal(t, "VA", attrs["db.response.status_code"].AsString())
_, legacySystem := attrs["db.system"]
require.False(t, legacySystem)
_, legacyOperation := attrs["db.operation"]
require.False(t, legacyOperation)
```

Update key and batch tests to expect `db.operation.parameter.key` and `db.operation.batch.size`. Add subtests proving batch size `0` and `1` are omitted.

- [ ] **Step 2: Add failing endpoint and error metadata tests**

Add table-driven endpoint tests for `host:11211`, `[2001:db8::1]:11211`, `cache.internal`, and `/var/run/memcached.sock`. Add error assertions for `error.type`, exception event, and error span status. Assert the instrumentation scope schema URL equals `semconv.SchemaURL`.

- [ ] **Step 3: Run adapter tests and verify RED**

Run: `cd otelmemcache && go test ./...`

Expected: FAIL because the adapter emits legacy database attributes, a combined endpoint, custom request/key attributes, and no error type or schema URL.

- [ ] **Step 4: Implement semantic-convention attributes**

Import `net`, `strconv`, and `semconv "go.opentelemetry.io/otel/semconv/v1.41.0"`. Build attributes with generated helpers:

```go
attrs := []attribute.KeyValue{
	semconv.DBSystemNameMemcached,
	semconv.DBOperationName(name),
}
attrs = append(attrs, serverAttributes(info.Server)...)
if info.Requests >= 2 {
	attrs = append(attrs, semconv.DBOperationBatchSize(info.Requests))
}
```

`serverAttributes` must preserve host-only and Unix-socket values, split valid TCP endpoints, and parse the port as an integer. Configure the tracer with `trace.WithSchemaURL(semconv.SchemaURL)`.

- [ ] **Step 5: Implement span naming, status, key, and error mapping**

Use `BATCH` as the batch operation name. Name spans `<operation> <server>` when a server is present and `<operation>` otherwise. In `End`, set `semconv.DBResponseStatusCodeKey` for a non-empty status, set `semconv.ErrorType(err)` for failures, then record the exception, set error status, and end the span. Use `semconv.DBOperationParameter("key", info.Key)` for opted-in key capture.

- [ ] **Step 6: Verify adapter tests and commit**

Run: `cd otelmemcache && go test ./...`

Expected: PASS.

Commit:

```bash
git add otelmemcache/otelmemcache.go otelmemcache/otelmemcache_test.go
git commit -m "Align tracing with OpenTelemetry conventions"
```

### Task 3: Cover the adapter in developer commands and CI

**Files:**
- Modify: `dev.yml`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Extend DevBuddy commands**

Make `ci`, `test`, and `lint` run both modules, preserving the root module first. Add:

```yaml
  test-otelmemcache:
    desc: Run OpenTelemetry adapter tests
    run: cd otelmemcache && go test -v ./...
```

The `ci` command must run `go mod tidy -diff`, golangci-lint, and `go test -race ./...` independently in both module directories.

- [ ] **Step 2: Add an adapter CI job**

Add an `otelmemcache` job with Go 1.25 and these steps in `otelmemcache/`: download dependencies, verify dependencies, check `go mod tidy -diff`, run `go test -v -race ./...`, and invoke golangci-lint-action with `working-directory: otelmemcache`.

- [ ] **Step 3: Verify YAML and commands locally**

Run:

```bash
bud test-otelmemcache
bud lint
go mod tidy -diff
(cd otelmemcache && go mod tidy -diff)
```

Expected: all commands exit zero and both linter invocations report zero issues.

- [ ] **Step 4: Commit workflow coverage**

```bash
git add dev.yml .github/workflows/ci.yml
git commit -m "Test the tracing adapter in CI"
```

### Task 4: Full verification

**Files:**
- Verify all modified files

- [ ] **Step 1: Run full test and lint checks**

Run:

```bash
bud ci
```

Expected: tidy checks are clean, both linters report zero issues, and root plus adapter race tests pass.

- [ ] **Step 2: Run focused benchmarks**

Run:

```bash
go test -run='^$' -bench='^BenchmarkClient/(Get|Get_Miss|Set|Delete|Increment)$' -benchmem -count=10 .
```

Compare allocations with the pre-change baseline. Expected: no additional allocation on the default no-op observer path.

- [ ] **Step 3: Inspect the final diff**

Run:

```bash
git diff --check origin/main...HEAD
git status --short --branch
git log --oneline origin/worktree-observability..HEAD
```

Expected: no whitespace errors, only intended files changed, and the worktree is clean after commits.
