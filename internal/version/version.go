// Package version holds RolloutProof's build identity: a semantic
// version, the git commit it was built from, and the build timestamp.
// All three default to "dev"/"unknown" for a plain `go build`/`go run`
// and are overridden at release-build time via -ldflags (see
// docs/RELEASING.md and .github/workflows/release.yml), the standard Go
// convention for embedding build metadata without a build-time config
// file.
package version

import "fmt"

// Version, Commit, and Date are set via:
//
//	go build -ldflags "-X github.com/SamudralaAjaykumarrr/rolloutproof/internal/version.Version=v0.1.0 \
//	  -X github.com/SamudralaAjaykumarrr/rolloutproof/internal/version.Commit=$(git rev-parse --short HEAD) \
//	  -X github.com/SamudralaAjaykumarrr/rolloutproof/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// A plain `go build ./cmd/rolloutproof` (or `go run`) leaves all three at
// their zero-override defaults below, which `rolloutproof version` must
// render honestly as "dev" rather than a fabricated release number.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String renders the one-line identity `rolloutproof version` prints and
// SARIF's tool.driver.version field embeds (internal/report.RenderSARIF).
func String() string {
	return fmt.Sprintf("rolloutproof %s (commit %s, built %s)", Version, Commit, Date)
}
