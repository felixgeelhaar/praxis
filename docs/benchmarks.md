# Praxis benchmarks

Release tag: `v0.5.0-dev`  
Captured: 2026-05-01T21:15:36Z  
Method: `make bench` (10 iterations × 1s) averaged per benchmark.

These numbers are the perf reference each release ships against. The build
fails when any benchmark regresses past 20% of the committed baseline
(`make bench-check` enforced in CI). To refresh the baseline, run
`make bench > bench/baseline.txt` and review the diff in PR.

| Benchmark | ns/op | µs/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| `BenchmarkDryRun_Memory` | 2673 | 2.67 | 5128 | 52 |
| `BenchmarkExecute_Memory` | 5947 | 5.95 | 8185 | 90 |
| `BenchmarkExecute_Memory_Parallel` | 4912 | 4.91 | 8465 | 89 |
| `BenchmarkProcessOpener_Execute` | 8172 | 8.17 | 3235 | 56 |
| `BenchmarkProcessOpener_Execute_Parallel` | 4887 | 4.89 | 3233 | 56 |

## Reading the table

- **ns/op / µs/op:** wall-clock latency per operation. Lower is better.
- **B/op:** bytes allocated on the heap per op. Heap pressure tracks GC cost.
- **allocs/op:** number of distinct heap allocations per op.

Out-of-process (`BenchmarkProcessOpener_*`) numbers measure the IPC tax
via in-process pipes — the codec + JSON marshalling cost — not subprocess
scheduling. Real subprocess scheduling adds OS-dependent overhead on top.
