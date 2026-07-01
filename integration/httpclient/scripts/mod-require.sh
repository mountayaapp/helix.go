#!/usr/bin/env bash

go mod edit \
  -require github.com/mountayaapp/helix.go@$1
