// Copyright 2026 Mert Öztürk
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mertdotcc/kubectl-probes/internal/model"
)

// The Summary's layout: how far its lines are indented under their heading,
// and how wide the widest histogram bar is.
const (
	summaryIndent = "  "
	barWidth      = 20
	barCell       = "█"
)

// percentileLabels name the two ends of the range, which are what a reader
// looks for first.
var percentileLabels = map[int]string{
	100: "P100 (max)",
	0:   "P0 (min)",
}

// summary writes the closing section of the Overview.
//
// It is uncoloured throughout. Failure detection has no bad direction: a long
// one sends traffic to a broken container for longer, and a short liveness one
// restarts containers that were only slow, so colouring either end would be an
// opinion inside a section of facts.
func summary(w io.Writer, s *model.Summary, opts Options) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Summary: %s in %s\n",
		plural(s.Containers, "container"), plural(s.Workloads, "workload"))

	fmt.Fprintln(&buf)
	coverage(&buf, s.Coverage)

	for _, section := range []struct {
		name string
		dist *model.Distribution
	}{
		{"Readiness", s.FailureDetection.Readiness},
		{"Liveness", s.FailureDetection.Liveness},
	} {
		if section.dist == nil {
			continue
		}
		fmt.Fprintln(&buf)
		if err := distribution(&buf, section.name, section.dist, opts); err != nil {
			return err
		}
	}

	_, err := w.Write(buf.Bytes())
	return err
}

// coverage is one line per probe, in the order ADR 0009 draws them, then the
// containers that have none at all.
func coverage(w io.Writer, c model.Coverage) {
	lines := []struct {
		name  string
		share model.Share
		note  string
	}{
		{"readiness", c.Readiness, ""},
		{"liveness", c.Liveness, ""},
		{"startup", c.Startup, ""},
		{"none", c.None, ""},
	}
	if c.ReadinessExcluded > 0 {
		lines[0].note = fmt.Sprintf("(%s not counted)", plural(c.ReadinessExcluded, "Job and CronJob container"))
	}

	digits := 1
	for _, line := range lines {
		digits = max(digits, len(strconv.Itoa(line.share.Of)))
	}

	fmt.Fprintln(w, "Coverage")
	for _, line := range lines {
		text := fmt.Sprintf("%s%-9s   %*d / %*d  %4s", summaryIndent, line.name,
			digits, line.share.Count, digits, line.share.Of, percent(line.share))
		if line.note != "" {
			text += "   " + line.note
		}
		fmt.Fprintln(w, text)
	}
}

// percent is a share rounded to the nearest whole percent. A share of nothing
// is a dash, because 0% of no containers would read as a finding.
func percent(s model.Share) string {
	if s.Of == 0 {
		return absent
	}
	return fmt.Sprintf("%d%%", (s.Count*200+s.Of)/(2*s.Of))
}

// distribution is one probe's failure detection: the container at each
// percentile, then the histogram.
func distribution(w io.Writer, name string, d *model.Distribution, opts Options) error {
	fmt.Fprintf(w, "%s failure detection (%s)\n", name, plural(d.Containers, "container"))

	// The leading empty cell is the indent, which the table then keeps.
	t := &table{}
	for _, p := range d.Percentiles {
		cells := []cell{plain(""), plain(percentileLabel(p.Percentile)), plain(short(p.FailureDetection))}
		if opts.AllNamespaces {
			cells = append(cells, plain(p.Namespace))
		}
		cells = append(cells, plain(p.Workload), plain(p.Container))
		t.add(cells...)
	}
	if err := t.write(w); err != nil {
		return err
	}

	fmt.Fprintln(w)
	histogram(w, d.Buckets)
	return nil
}

func percentileLabel(p int) string {
	if label, ok := percentileLabels[p]; ok {
		return label
	}
	return "P" + strconv.Itoa(p)
}

// histogram draws one bar per bucket, scaled to the fullest one. The count
// beside each bar is exact; the bar is only how it compares.
func histogram(w io.Writer, buckets []model.Bucket) {
	fullest, labelWidth := 0, 0
	labels := bucketLabels(buckets)
	for i, b := range buckets {
		fullest = max(fullest, b.Count)
		labelWidth = max(labelWidth, utf8.RuneCountInString(labels[i]))
	}
	digits := len(strconv.Itoa(fullest))

	for i, b := range buckets {
		cells := 0
		if fullest > 0 {
			cells = int(math.Round(float64(b.Count) * barWidth / float64(fullest)))
		}
		bar := strings.Repeat(barCell, cells) + strings.Repeat(" ", barWidth-cells)
		fmt.Fprintf(w, "%s%*s    %s  %*d\n", summaryIndent, labelWidth, labels[i], bar, digits, b.Count)
	}
}

// bucketLabels reads each bucket as the range it covers: at most its bound,
// more than the bound before it. A lower bound in the same unit as the upper
// one drops it, so 10–30s rather than 10s–30s.
func bucketLabels(buckets []model.Bucket) []string {
	labels := make([]string, len(buckets))
	var lower string
	for i, b := range buckets {
		switch {
		case b.UpTo == nil:
			labels[i] = ">" + lower
		case lower == "":
			labels[i] = "≤" + short(*b.UpTo)
		default:
			upper := short(*b.UpTo)
			from := lower
			if from[len(from)-1] == upper[len(upper)-1] {
				from = from[:len(from)-1]
			}
			labels[i] = from + "–" + upper
		}
		if b.UpTo != nil {
			lower = short(*b.UpTo)
		}
	}
	return labels
}
