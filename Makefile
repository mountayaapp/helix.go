.PHONY: init lint test vulncheck release

#
# Install and replace required dependencies.
#
init:
	bash ./scripts/mod-tidy.sh

#
# Run linters across all Go modules/packages.
#
lint:
	bash ./scripts/modules.sh lint

#
# Run tests across all Go modules/packages.
#
test:
	bash ./scripts/modules.sh test

#
# Report known vulnerabilities reachable from any Go module.
#
vulncheck:
	bash ./scripts/modules.sh vulncheck

#
# Run the gate, then commit, tag, and push a new version, and restore the local
# replaces: make release VERSION=v0.26.0. Preview it first, changing nothing,
# with bash ./scripts/release.sh --dry-run v0.26.0.
#
release:
	bash ./scripts/release.sh $(VERSION)
