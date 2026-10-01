VERSION := $(shell git describe --tags --match 'v*' --dirty --always)
COMMIT  := $(shell git rev-parse --short HEAD)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/openfloorcontrol/ofc/cmd
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: build install test web release-check release-snapshot release

build: web
	cd cli && go build -ldflags "$(LDFLAGS)" -o ofc .

install: web
	cd cli && go install -ldflags "$(LDFLAGS)" .

test:
	cd cli && go test ./...

web:
	cd web && { [ -d node_modules ] || npm ci; } && npm run build

# Releasing (GoReleaser builds archives, the GitHub release, and the
# Homebrew cask in openfloorcontrol/homebrew-tap):
#   1. make release-check
#   2. git tag vX.Y.Z && git push origin vX.Y.Z
#   3. GITHUB_TOKEN=... make release   (token needs write on ofc and homebrew-tap)
release-check:
	@command -v goreleaser >/dev/null || { echo "goreleaser not installed"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "working tree not clean"; exit 1; }
	goreleaser check
	cd cli && go test ./...

release-snapshot:
	goreleaser release --snapshot --clean

release:
	goreleaser release --clean
