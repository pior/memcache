# Releasing

A release is a git tag. The module proxy serves it; there is nothing to build or
upload. A GitHub Release is created from the tag as well, because pkg.go.dev
shows no changelog of its own — the Releases page is where a human, and
Dependabot's upgrade PR body, find out what changed.

## Versioning

Pre-v1.0, so the `go` command applies no compatibility guarantee and neither do
we: **a breaking change bumps the minor version** (v0.1.0 → v0.2.0) and is
listed under "Changed" or "Removed" in the changelog. Patch versions are fixes
only.

`otelmemcache` is versioned in lockstep with the core, even when nothing in it
changed. One number to reason about, and a caller cannot assemble a mismatched
pair.

## Modules

| module | tag | published |
|---|---|---|
| `github.com/pior/memcache` | `vX.Y.Z` | yes |
| `github.com/pior/memcache/otelmemcache` | `otelmemcache/vX.Y.Z` | yes |
| `loadtest/`, `stress/`, `cmd/bench/` | — | no, development tooling |

The unpublished modules keep their `replace github.com/pior/memcache => ..`
and are never tagged.

`otelmemcache` is different because someone outside this repository resolves it.
A `replace` directive is **ignored** in any module that is not the main module,
so a consumer sees only the `require github.com/pior/memcache vX.Y.Z` line — and
that version must be a tag that exists, or their build fails with
`invalid version: unknown revision`. Bumping it is a release step: `go mod tidy`
will not do it, because the replace makes tidy resolve against the working tree.

## Steps

1. Open a release PR that:
   - moves the `Unreleased` entries of `CHANGELOG.md` under the new version and
     today's date, and adds the two link definitions at the bottom;
   - sets `require github.com/pior/memcache vX.Y.Z` in `otelmemcache/go.mod` to
     the version being released.
2. Merge it once CI is green.
3. Tag the merge commit and push both tags:

   ```bash
   git switch main && git pull
   git tag vX.Y.Z
   git tag otelmemcache/vX.Y.Z
   git push origin vX.Y.Z otelmemcache/vX.Y.Z
   ```

4. `.github/workflows/release.yml` runs per tag. It re-runs that module's tests
   against the tagged tree, checks that `otelmemcache` requires a core version
   that is actually tagged, and creates the GitHub Release with that version's
   changelog section as the body.
5. Confirm the proxy serves both:

   ```bash
   GOPROXY=proxy.golang.org go list -m github.com/pior/memcache@vX.Y.Z
   GOPROXY=proxy.golang.org go list -m github.com/pior/memcache/otelmemcache@vX.Y.Z
   ```

## A tag is permanent

The proxy caches a tag the first time anyone fetches it, and moving or deleting
the tag afterwards does not change what it serves. A bad release is fixed by
tagging the next patch version and adding a `retract` block to `go.mod` for the
bad one — never by re-pointing a tag.
