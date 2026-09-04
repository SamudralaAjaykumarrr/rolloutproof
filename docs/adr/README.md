# Architecture Decision Records

Each ADR records one consequential design decision: the alternatives
actually considered, why the chosen one won, and what it costs. ADRs are
not updated after acceptance — a reversed decision gets a new ADR that
supersedes the old one, so the historical record of *why* stays intact.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-normalized-ir-over-direct-rule-checks.md) | Normalized IR over direct rule checks on raw artifacts | Accepted |
| [0002](0002-transition-graph-over-sequential-model.md) | Transition graph over a simple sequential before/after model | Accepted |
| [0003](0003-constrained-reachability-over-full-enumeration.md) | Constrained reachability over full state enumeration | Accepted |
| [0004](0004-explicit-dependency-metadata-over-source-analysis.md) | Explicit dependency metadata over source-code analysis | Accepted |
| [0005](0005-tristate-verdict-over-boolean.md) | Tri-state SAFE/UNSAFE/UNKNOWN verdict over boolean safe/unsafe | Accepted |
| [0006](0006-structured-evidence-over-hardcoded-diagnostics.md) | Structured evidence/counterexample model over hard-coded diagnostics | Accepted |
| [0007](0007-v1-contract-metadata-format.md) | V1 service/schema contract metadata format | Accepted |
| [0008](0008-zero-value-safety-for-verdict-enums.md) | Zero-value safety for verdict and classification enums | Accepted |
