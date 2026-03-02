#!/usr/bin/env bash

go mod edit \
  -require github.com/mountayaapp/helix.go@$1 \
  -require github.com/mountayaapp/helix.go/integration/valkey@$1
