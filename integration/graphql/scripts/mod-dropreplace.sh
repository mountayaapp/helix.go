#!/usr/bin/env bash

go mod edit \
  -dropreplace github.com/mountayaapp/helix.go \
  -dropreplace github.com/mountayaapp/helix.go/integration/valkey
