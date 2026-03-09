.PHONY: init lint tests release

#
# Install and replace required dependencies.
#
init:
	bash ./scripts/mod-tidy.sh

#
# Run linters across all Go modules/packages.
#
lint:
	bash ./scripts/lint.sh

#
# Run tests across all Go modules/packages.
#
test:
	bash ./scripts/test.sh

#
# Commit, tag, release, and update dependencies when releasing a new version.
#
release:
	bash ./scripts/release.sh
