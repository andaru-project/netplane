// SPDX-License-Identifier: AGPL-3.0-or-later
package rtlplayground

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// sample is one Prometheus text-format sample.
type sample struct {
	name   string
	labels map[string]string
	value  float64
	raw    string // value as written, for exact 64-bit counters
}

// parsePromText reads the Prometheus text exposition format (0.0.4) the firmware's exporter
// writes. Comments, timestamps and unparsable lines are skipped: a partial scrape still
// yields every sample it holds.
func parsePromText(r io.Reader) ([]sample, error) {
	var out []sample
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if s, ok := parseSample(line); ok {
			out = append(out, s)
		}
	}
	return out, sc.Err()
}

func parseSample(line string) (sample, bool) {
	s := sample{labels: map[string]string{}}
	i := strings.IndexAny(line, "{ \t")
	if i <= 0 {
		return s, false
	}
	s.name = line[:i]
	rest := line[i:]
	if rest[0] == '{' {
		var ok bool
		if rest, ok = parseLabels(rest[1:], s.labels); !ok {
			return s, false
		}
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return s, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return s, false
	}
	s.value, s.raw = v, fields[0]
	return s, true
}

// parseLabels parses `k="v",...}` into dst and returns the text after the closing brace.
func parseLabels(in string, dst map[string]string) (string, bool) {
	for {
		in = strings.TrimLeft(in, " ,")
		if strings.HasPrefix(in, "}") {
			return in[1:], true
		}
		eq := strings.IndexByte(in, '=')
		if eq <= 0 || len(in) < eq+2 || in[eq+1] != '"' {
			return "", false
		}
		key := strings.TrimSpace(in[:eq])
		in = in[eq+2:]
		var b strings.Builder
		closed := false
		for j := 0; j < len(in); j++ {
			c := in[j]
			if c == '\\' && j+1 < len(in) {
				j++
				switch in[j] {
				case 'n':
					b.WriteByte('\n')
				default:
					b.WriteByte(in[j])
				}
				continue
			}
			if c == '"' {
				in = in[j+1:]
				closed = true
				break
			}
			b.WriteByte(c)
		}
		if !closed {
			return "", false
		}
		dst[key] = b.String()
	}
}

// String renders a sample for test failure messages.
func (s sample) String() string { return fmt.Sprintf("%s%v %g", s.name, s.labels, s.value) }
