#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

go test \
	-run '^$' \
	-bench '^(BenchmarkInboundConfigMarshalJSON|BenchmarkConfigEquals|BenchmarkStatsNameParse|BenchmarkAggregateTraffic|BenchmarkApplyClients)$' \
	-benchmem \
	./core/singbox ./database/model \
	"$@"
