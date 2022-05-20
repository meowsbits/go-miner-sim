#!/usr/bin/env bash

mkdir -p test_results
go test -v -run TestPlotting . |& tee test_results/log_$(date +%s).txt
