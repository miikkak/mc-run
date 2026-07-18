#!/usr/bin/env bash
set -Eeuo pipefail
trap 'echo "Error on line $LINENO"' ERR

go test ./... -race -cover
