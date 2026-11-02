// Command recv reads tailer output from standard input and checks it.
//
// It runs outside the measured cgroup. Every approach under test writes each
// line to its standard output, and the driver pipes that stream into this
// program. The receiver reads the TS and N markers and the ASD suffix. It does
// not parse JSON. It scans the raw bytes, so it works for a tool that wraps the
// line in a JSON envelope, and for a tool that batches several records on one
// output line.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

// indexFrom returns the index of sub in b at or after start, or -1.
func indexFrom(b []byte, sub string, start int) int {
	s := []byte(sub)
	for i := start; i+len(s) <= len(b); i++ {
		match := true
		for j := 0; j < len(s); j++ {
			if b[i+j] != s[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// parseDigits reads the decimal integer at start. It returns the value and the
// index after the last digit. found is false when start is not a digit.
func parseDigits(b []byte, start int) (v int64, end int, found bool) {
	end = start
	for ; end < len(b) && b[end] >= '0' && b[end] <= '9'; end++ {
		v = v*10 + int64(b[end]-'0')
		found = true
	}
	return v, end, found
}

func percentiles(d []time.Duration) (p50, p99 time.Duration) {
	if len(d) == 0 {
		return 0, 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)/2], d[len(d)*99/100]
}

func main() {
	warmup := flag.Int("warmup", 0, "ignore sequence numbers below this value")
	flag.Parse()

	seen := map[int64]int{}
	var lats []time.Duration
	received, dup, badWork, noMarker := 0, 0, 0, 0

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		b := sc.Bytes()
		now := time.Now()
		// A line may hold several records, so scan every TS=/N= pair.
		pos := 0
		pairs := 0
		for {
			i := indexFrom(b, "TS=", pos)
			if i < 0 {
				break
			}
			ts, p1, okTS := parseDigits(b, i+3)
			if !okTS {
				pos = i + 3
				continue
			}
			j := indexFrom(b, "N=", p1)
			if j < 0 {
				break
			}
			n, p2, okN := parseDigits(b, j+2)
			if !okN {
				pos = j + 2
				continue
			}
			pairs++
			pos = p2
			// The record runs until the next TS= marker or the line end.
			segEnd := indexFrom(b, "TS=", p2)
			if segEnd < 0 {
				segEnd = len(b)
			}
			if indexFrom(b[:segEnd], "ASD", p2) < 0 {
				badWork++
			}
			received++
			if int(n) < *warmup {
				continue
			}
			seen[n]++
			if seen[n] == 1 {
				lats = append(lats, now.Sub(time.Unix(0, ts)))
			} else {
				dup++
			}
		}
		if pairs == 0 {
			noMarker++
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "recv: read:", err)
	}

	p50, p99 := percentiles(lats)
	fmt.Printf("RESULT received=%d distinct=%d dup=%d badwork=%d nomarker=%d p50ms=%.3f p99ms=%.3f\n",
		received, len(seen), dup, badWork, noMarker,
		float64(p50)/float64(time.Millisecond), float64(p99)/float64(time.Millisecond))
}
