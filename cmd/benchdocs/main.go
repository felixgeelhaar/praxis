// Command benchdocs renders raw `go test -bench` output into a
// human-readable docs/benchmarks.md. The bench-publish workflow runs
// it on tag push so each release ships a fresh perf snapshot
// alongside the binary.
//
// Usage:
//
//	benchdocs -input bench/release.txt -output docs/benchmarks.md -tag v0.5.0
//
// Phase 5 perf benchmarks.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	input := flag.String("input", "bench/release.txt", "raw `go test -bench` output")
	output := flag.String("output", "docs/benchmarks.md", "destination markdown file")
	tag := flag.String("tag", "", "release tag to embed in the document header")
	flag.Parse()

	stats, err := loadAverages(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	if err := render(*output, *tag, stats); err != nil {
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
}

type stat struct {
	NsPerOp     float64
	BytesPerOp  float64
	AllocsPerOp float64
}

// loadAverages parses raw `go test -bench` output and returns per-name
// averages over the captured -count iterations. Mirrors cmd/benchcheck
// but additionally captures B/op and allocs/op so the rendered table
// surfaces allocation pressure alongside latency.
func loadAverages(path string) (map[string]stat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	type acc struct {
		ns, b, a float64
		count    int
	}
	accs := map[string]*acc{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		name := stripCPUSuffix(fields[0])
		ns := readQuantity(fields, "ns/op")
		bo := readQuantity(fields, "B/op")
		al := readQuantity(fields, "allocs/op")
		if ns == 0 {
			continue
		}
		s, ok := accs[name]
		if !ok {
			s = &acc{}
			accs[name] = s
		}
		s.ns += ns
		s.b += bo
		s.a += al
		s.count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]stat, len(accs))
	for name, s := range accs {
		if s.count == 0 {
			continue
		}
		out[name] = stat{
			NsPerOp:     s.ns / float64(s.count),
			BytesPerOp:  s.b / float64(s.count),
			AllocsPerOp: s.a / float64(s.count),
		}
	}
	return out, nil
}

func render(path, tag string, stats map[string]stat) error {
	names := make([]string, 0, len(stats))
	for n := range stats {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "# Praxis benchmarks\n\n")
	if tag != "" {
		fmt.Fprintf(&b, "Release tag: `%s`  \n", tag)
	}
	fmt.Fprintf(&b, "Captured: %s  \n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Method: `make bench` (10 iterations × 1s) averaged per benchmark.\n\n")
	fmt.Fprintf(&b, "These numbers are the perf reference each release ships against. The build\n")
	fmt.Fprintf(&b, "fails when any benchmark regresses past 20%% of the committed baseline\n")
	fmt.Fprintf(&b, "(`make bench-check` enforced in CI). To refresh the baseline, run\n")
	fmt.Fprintf(&b, "`make bench > bench/baseline.txt` and review the diff in PR.\n\n")
	fmt.Fprintf(&b, "| Benchmark | ns/op | µs/op | B/op | allocs/op |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|\n")
	for _, n := range names {
		s := stats[n]
		fmt.Fprintf(&b, "| `%s` | %.0f | %.2f | %.0f | %.0f |\n",
			n, s.NsPerOp, s.NsPerOp/1000.0, s.BytesPerOp, s.AllocsPerOp)
	}
	fmt.Fprintf(&b, "\n## Reading the table\n\n")
	fmt.Fprintf(&b, "- **ns/op / µs/op:** wall-clock latency per operation. Lower is better.\n")
	fmt.Fprintf(&b, "- **B/op:** bytes allocated on the heap per op. Heap pressure tracks GC cost.\n")
	fmt.Fprintf(&b, "- **allocs/op:** number of distinct heap allocations per op.\n\n")
	fmt.Fprintf(&b, "Out-of-process (`BenchmarkProcessOpener_*`) numbers measure the IPC tax\n")
	fmt.Fprintf(&b, "via in-process pipes — the codec + JSON marshalling cost — not subprocess\n")
	fmt.Fprintf(&b, "scheduling. Real subprocess scheduling adds OS-dependent overhead on top.\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func stripCPUSuffix(s string) string {
	idx := strings.LastIndexByte(s, '-')
	if idx < 0 {
		return s
	}
	if _, err := strconv.Atoi(s[idx+1:]); err != nil {
		return s
	}
	return s[:idx]
}

// readQuantity finds the float that precedes the unit string (e.g.
// "ns/op", "B/op"). Returns 0 if absent.
func readQuantity(fields []string, unit string) float64 {
	for i, f := range fields {
		if f == unit && i > 0 {
			v, err := strconv.ParseFloat(fields[i-1], 64)
			if err == nil {
				return v
			}
		}
	}
	return 0
}
