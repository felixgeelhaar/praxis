// Command benchcheck compares two `go test -bench` outputs and exits
// non-zero when any matching benchmark regresses past the configured
// threshold. Used by `make bench-check` and CI to fail builds that
// would silently slow down the executor or out-of-process loader.
//
// Usage:
//
//	benchcheck -baseline bench/baseline.txt -current bench/current.txt -threshold 1.20
//
// Threshold is a multiplier on baseline ns/op; 1.20 means "fail when
// current is more than 20% slower than baseline." Both files must be
// raw `go test -bench` output (the same format `make bench` produces).
//
// Phase 5 perf benchmarks.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func main() {
	baseline := flag.String("baseline", "bench/baseline.txt", "baseline `go test -bench` output")
	current := flag.String("current", "bench/current.txt", "current `go test -bench` output")
	threshold := flag.Float64("threshold", 1.20, "fail when current/baseline ns/op exceeds this multiplier")
	flag.Parse()

	base, err := loadBench(*baseline)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load baseline:", err)
		os.Exit(2)
	}
	cur, err := loadBench(*current)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load current:", err)
		os.Exit(2)
	}

	regressions := 0
	missing := 0
	fmt.Printf("%-50s %12s %12s %8s\n", "benchmark", "baseline_ns", "current_ns", "ratio")
	for name, bNs := range base {
		cNs, ok := cur[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "missing in current: %s\n", name)
			missing++
			continue
		}
		ratio := cNs / bNs
		marker := ""
		if ratio > *threshold {
			marker = "  <-- REGRESSION"
			regressions++
		}
		fmt.Printf("%-50s %12.0f %12.0f %8.2fx%s\n", name, bNs, cNs, ratio, marker)
	}
	if missing > 0 {
		fmt.Fprintf(os.Stderr, "%d benchmarks missing in current; pin or rename baseline\n", missing)
		os.Exit(1)
	}
	if regressions > 0 {
		fmt.Fprintf(os.Stderr, "%d regression(s) above %.0f%% threshold\n", regressions, (*threshold-1)*100)
		os.Exit(1)
	}
	fmt.Println("OK: no regressions above threshold")
}

// loadBench parses `go test -bench` output into a map of benchmark
// name -> ns/op. Multiple runs of the same benchmark (from -count > 1)
// are averaged so callers get a single ns/op per name without pulling
// in benchstat as a dependency.
func loadBench(path string) (map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	type stat struct {
		sum   float64
		count int
	}
	stats := map[string]*stat{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		// Format: BenchmarkName-CPU N ns/op [B/op] [allocs/op]
		if len(fields) < 4 {
			continue
		}
		name := stripCPUSuffix(fields[0])
		nsField := -1
		for i, f := range fields {
			if f == "ns/op" && i > 0 {
				nsField = i - 1
				break
			}
		}
		if nsField < 0 {
			continue
		}
		ns, err := strconv.ParseFloat(fields[nsField], 64)
		if err != nil {
			continue
		}
		s, ok := stats[name]
		if !ok {
			s = &stat{}
			stats[name] = s
		}
		s.sum += ns
		s.count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(stats))
	for name, s := range stats {
		if s.count > 0 {
			out[name] = s.sum / float64(s.count)
		}
	}
	return out, nil
}

// stripCPUSuffix removes the -CPU concurrency suffix Go appends to
// every benchmark name (e.g. "BenchmarkExecute_Memory-10" → name).
// Keeps the suffix when no dash is present.
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
