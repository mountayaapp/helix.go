#!/usr/bin/env bash

go mod edit \
  -replace github.com/mountayaapp/helix.go=../../ \
  -replace github.com/mountayaapp/helix.go/integration/valkey=../valkey
