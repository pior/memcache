# Formal models

TLA+ models of the parts of the client where bugs come from interleavings and
peer misbehaviour rather than from inputs: the pool registry, the connection
lifecycle, stream synchronisation and breaker attribution. Fuzzing covers the
pure functions (parser, serializer, hashing). Nothing here verifies the Go code
directly: each model is a hand-written abstraction of named Go functions, kept
honest by the seeded mutants below.

## Running

`bud formal` checks every model; `bud formal formal/<model>` checks one.
CI runs the same script (`.github/workflows/formal.yml`).

`formal/tlc.sh` needs a Java 11+ runtime (`bud up` installs Homebrew's
`openjdk`; `JAVA_HOME` overrides it). It downloads `tla2tools.jar` v1.7.4 into
`~/.cache/tla2tools/` on first use and checks its SHA-256. `TLA2TOOLS_JAR`
points it at another copy.

## Layout

One directory per model:

- `<Spec>.tla`: the model, usually PlusCal with its TLA+ translation committed
  below it. Its header names the Go functions it models and what it leaves
  out.
- `<Spec>.cfg`: the bounds and the properties checked. TLC must find no error.
- `<Spec>.mutant-<name>.cfg`: the same check with a seeded bug switched on
  through the `Mutant` constant. The first line names the violation TLC must
  report: `\* expect: <Invariant>`, `\* expect: liveness` or
  `\* expect: deadlock`.

A model that checks clean proves nothing until it is shown to catch bugs, so
the mutants are part of the check: if one stops being caught, `tlc.sh` fails.
Prefer mutants that re-introduce a bug this repository actually had.

## Editing a model

1. Edit the PlusCal algorithm (inside the `(* --algorithm ... *)` comment).
2. `bud formal-translate` regenerates the TLA+ between `BEGIN TRANSLATION` and
   `END TRANSLATION`. Never edit that part by hand: `tlc.sh` fails when it is
   stale.
3. Properties that read process-local variables cannot go in the algorithm's
   `define` block; put them after `END TRANSLATION`.
4. `bud formal`.

When a counterexample reveals a real defect, the fix ships with a Go
regression test, like any other bug.

## Models

| Model | Go code | Properties |
|-------|---------|------------|
| `serverpools` | `serverPools.getOrCreate`, `closeAll`, `Client.Close` | no pool left open after `Close`; an operation started after `Close` gets `ErrClientClosed`; termination |
| `connsync` | `Connection.Execute`, `ExecuteBatch`, `checkExecutable`, `ServerPool.execRequestDirect`, `execBatchDirect`, `Commands.executeBatch`, `meta.ShouldCloseConnection` | no reply reaches a request it was not produced for; no connection returns to the pool owing a reply; a batch never holds more replies than requests |

Planned: the full pool lifecycle with reaping and the maintenance pass, and
breaker timeout attribution.
