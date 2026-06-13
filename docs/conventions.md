# JOG Development Conventions

This document collects repeatable conventions for contributing to JOG. It
complements the high-level guidelines in [`CLAUDE.md`](../CLAUDE.md); where the
two overlap, `CLAUDE.md` is the source of truth and this document links back to
it instead of restating the details.

## New S3 Subresource API Checklist

Adding a new S3 subresource API (for example a `?tagging`, `?lifecycle`,
`?versioning`, `?acl`, `?policy`, or `?website` operation) touches several
layers in a predictable order. Use this checklist for every new subresource
API so nothing is skipped. Follow the project's TDD (Red-Green-Refactor) cycle:
write the failing test first, then implement until it passes.

### 1. Tests first (Red)

- [ ] **AWS SDK v2 round-trip test** - Add a test in `test/s3compat/` that
  exercises the operation through `github.com/aws/aws-sdk-go-v2` (PUT then GET
  then DELETE where applicable) so the behavior is verified against real S3
  client expectations. Name the file after the feature
  (e.g. `test/s3compat/<feature>_test.go`).
- [ ] **Error compatibility test** - Add a test that asserts the S3 error code
  and HTTP status returned for the failure cases (missing bucket/key, malformed
  body, unsupported request). Error codes and status codes must match AWS S3
  (see the "S3 API Implementation Notes" section of [`CLAUDE.md`](../CLAUDE.md)).
  `test/s3compat/error_test.go` shows the existing pattern.

### 2. Handler (Green)

- [ ] **Add the handler** - Implement the operation as a
  `func (h *Handler) <Operation>(w http.ResponseWriter, r *http.Request)`
  method in a feature-named file under `internal/api/`
  (e.g. `internal/api/<feature>.go`). Match the existing handlers for request
  parsing, XML response shape, and error responses
  (`internal/api/errors.go`).
- [ ] **Add the router branch** - Wire the operation into
  `internal/server/router.go`. Subresources are dispatched by query parameter
  via `query.Has("<subresource>")` inside the method (GET/PUT/DELETE) and
  bucket-vs-object branches. Place the new branch consistently with the
  surrounding cases.

### 3. Storage layer

- [ ] **Add the storage interface method(s)** - Declare the operation on the
  `Storage` interface in `internal/storage/interface.go` and implement it in
  `internal/storage/filesystem.go` / `internal/storage/metadata.go`. Add any
  required value types (configuration structs, enums) to `interface.go`.
- [ ] **SQLite table + `ON DELETE CASCADE`** - If the subresource needs a new
  metadata table, add the `CREATE TABLE IF NOT EXISTS` statement in
  `internal/storage/metadata.go`. Any table keyed by bucket (or by another
  parent row) **must** declare
  `FOREIGN KEY (bucket) REFERENCES buckets(name) ON DELETE CASCADE`
  (or the appropriate parent) so subresource rows are cleaned up when the
  parent is deleted. Confirm `PRAGMA foreign_keys` enforcement is active and
  that deleting a bucket removes the new rows (cover this in a test).

### 4. Documentation (one-shot update)

When the implementation is complete, update all of the following together so the
docs do not drift. The canonical list of doc-update steps and the README badge
color rules live in the **Documentation Updates** section of
[`CLAUDE.md`](../CLAUDE.md) - follow that section rather than a copy here:

- [ ] `SPEC.md` - Document the new operation in the specification.
- [ ] `TODO.md` - Mark the implemented feature as completed.
- [ ] `docs/S3_API_CHECKLIST.md` - Update the operation's implementation status
  and the summary statistics table.
- [ ] `README.md` - Update the S3 API coverage badge percentage and color (see
  the Badge Color Guide in [`CLAUDE.md`](../CLAUDE.md)).

### 5. Gates

- [ ] `go build ./...` succeeds.
- [ ] `go test -count=1 ./internal/...` is green.
- [ ] `go test -count=1 ./test/...` is green (S3 compatibility tests).
- [ ] `gofmt -l <changed .go files>` prints nothing.
