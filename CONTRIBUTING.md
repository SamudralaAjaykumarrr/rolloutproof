# Contributing to RolloutProof

Thanks for considering a contribution. This document covers the
practical mechanics; `docs/vision.md` and `docs/architecture.md` cover
*why* the project is shaped the way it is — read those first if you're
proposing anything beyond a small fix, since a design that contradicts
them needs to argue against an existing, deliberate decision (see
"Proposing a design change" below).

## Before you start

Read, in order:

1. `README.md` — what RolloutProof is and current scope.
2. `docs/vision.md` — the problem, non-goals, and safety philosophy
   ("never silently SAFE" is the one rule everything else follows from).
3. `docs/architecture.md` — the IR, transition graph, and package layout.
4. `docs/invariants.md` — the invariant catalog, and what "adding an
   invariant" actually means (a new, stable ID — never redefining an
   existing one).
5. `docs/adr/` — accepted decisions and the alternatives already
   considered and rejected. If your proposal is one of the rejected
   alternatives, the relevant ADR is where to start the argument, not
   a PR.

## Development setup

```bash
git clone https://github.com/SamudralaAjaykumarrr/rolloutproof
cd rolloutproof
go build ./...
go test ./...
```

No external services, containers, or network access are needed to build
or test this project (`docs/vision.md` §5: RolloutProof itself is local
and offline, and so is its own test suite).

## Making a change

1. **Small fixes** (typos, a clearly-described bug with a minimal
   repro): open a PR directly.
2. **New invariants, IR fields, or parser support**: open an issue
   first describing the gap and citing the specific `docs/invariants.md`
   or `docs/architecture.md` section it extends, unless one already
   exists. This project's stated principle is "derive from evidence,
   never scenario names" (`docs/vision.md`) — a new check needs to name
   the IR facts it reasons over before it's implemented, not after.
3. **Proposing a design change** (a new package boundary, a changed IR
   type, a different graph construction strategy): open an issue linking
   the specific ADR(s) it would supersede. `docs/adr/README.md` explains
   the ADR process this project follows; a design change that doesn't
   engage with why the current approach was chosen is unlikely to be
   accepted as-is.

## Required before opening a PR

```bash
gofmt -l .                 # must print nothing
go vet ./...
go build ./...
go test ./...
go test -race ./...
go run ./cmd/eval           # scenario corpus must still be 100% pass
git diff --check            # no whitespace/merge-marker issues
```

This is exactly what `.github/workflows/ci.yml` runs — if it passes
locally, it will pass in CI.

### Adding a new invariant

- Give it the next unused ID in its family (`docs/invariants.md`'s own
  "Notes on catalog evolution": IDs are stable and versioned; a
  corrected rule gets a new ID, never a silent redefinition of an old
  one).
- It must reason only over `internal/ir` types — never raw YAML/SQL
  strings (`docs/architecture.md` §1's litmus test: if an invariant
  needs to switch on a YAML node kind or regex a SQL string, the
  missing fact belongs in the IR, not the invariant).
- Missing evidence must produce `ir.VerdictUnknown` with a specific
  `ir.EvidenceGap` (field + reason) — never a silent `VerdictSafe`
  fallback, and never a generic "insufficient data" message.
- Add both a package-level unit test (hand-built IR, in
  `internal/invariant`) and, where the scenario is one of
  `docs/scenario-corpus.md`'s documented cases, a real end-to-end
  fixture under `examples/{safe,unsafe,unknown}/<name>/` proven through
  `internal/verify` — see any existing `examples/*/` directory for the
  directory convention.
- Update `internal/invariant/doc.go`'s package comment to list the new
  ID; if it changes what the overall README status section claims,
  update that too. Docs and implementation must not silently diverge.

### Adding scenario-corpus fixtures

Every fixture under `examples/{safe,unsafe,unknown}/` is picked up
automatically by `cmd/eval` (`go run ./cmd/eval`), which checks the
fixture's actual verdict against its category directory. If you add a
fixture, it must pass that check — a fixture that doesn't demonstrate
what its directory name and any accompanying test claims is worse than
no fixture at all.

### Regression tests for bugs

If you're fixing a bug (including one found through your own testing,
not just a filed issue), add a test that would have failed before your
fix and passes after. This project's own history
(`git log --grep="Adversarial-review finding"`) is full of exactly this
pattern — a defect found, fixed, and pinned with a test in the same
commit.

## Commit style

- Focused commits: one logical change per commit, not a mixed bag.
- A commit message explaining *why*, not just *what* — the diff already
  shows what changed.
- Never force-push, `git reset --hard`, or rewrite shared history on a
  branch others may have based work on.

## Reporting a bug

Open an issue with:

- The exact command you ran and the directory/fixture (or a minimal
  reproduction of one) that triggers it.
- What you expected (citing the relevant doc/invariant, if applicable)
  versus what actually happened.
- Output of `rolloutproof version`.

If the "bug" is a verdict you disagree with, please first check
`docs/FALSE_POSITIVES.md` — it documents the precision boundaries and
known model gaps this project is already aware of; if your case isn't
already listed there, it's a genuinely useful report. `docs/review-start.md`
and `docs/ADVERSARIAL_REVIEW.md` ("How to challenge a verdict") walk
through this in more depth, and the "Break RolloutProof" issue template
(`.github/ISSUE_TEMPLATE/break-rolloutproof.yml`) is the preferred way to
report a false SAFE, false UNSAFE, or false UNKNOWN.

For a suspected **security** issue, see `SECURITY.md` instead of
opening a public issue.

## Code of Conduct

This project follows the `CODE_OF_CONDUCT.md` (Contributor Covenant).
