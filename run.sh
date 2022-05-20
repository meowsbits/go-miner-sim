#!/usr/bin/env bash

set -e

main() {
  git add .
  git commit -Ss -m "Pre-test commit." || echo "Nothing to commit. OK."

  ts="$(date +%s)"
  head=$(git rev-parse HEAD | cut -c 1-8)

  mkdir -p "out/test-stdout"
  go test -v -run TestPlotting |& tee out/test-stdout/output_${ts}_${head}.txt

  git add .
  git commit -Ss -m "Post-test commit: ${ts}_${head}"
}

main
