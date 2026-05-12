// Server-side SVG sparkline renderer.
//
// The output is a minimal 200x60 SVG containing a single <polyline>
// plus two short ticks marking the min and max y-positions. No text
// labels — the client adds the value + unit underneath the SVG so the
// label theme matches the rest of the SPA's tailwind classes. The
// caller hands the SVG bytes straight to the response as
// data.sparkline_svg (a string), and the React component drops them
// into the DOM with dangerouslySetInnerHTML.
//
// Why server-side: we want zero client charting dependency. Chart.js
// / Recharts / etc. add ~70KB+ gzipped per dashboard load; a static
// SVG is ~500 bytes and renders before the next animation frame.
//
// Design constraints (matches the task spec):
//   - 200x60 canvas with 4px padding on every side
//   - single polyline (no fill underneath) to keep the visual quiet
//     next to other UI elements
//   - min and max are marked by a 2-px horizontal tick at the y-position
//     of the corresponding sample
//   - degenerate inputs (empty matrix, single sample, all-equal samples)
//     render an empty SVG so the dashboard grid keeps its layout

package dashboards

import (
	"bytes"
	"fmt"
)

// RenderSparkline produces an SVG byte slice for the given matrix. The
// SVG is well-formed XML — the caller is responsible for setting the
// response content-type to image/svg+xml if they intend to serve it
// directly; the widget render response embeds it inside the data.json
// object as a string.
func RenderSparkline(matrix PromMatrix) []byte {
	const (
		width   = 200
		height  = 60
		padX    = 4
		padY    = 4
	)
	innerW := float64(width - 2*padX)
	innerH := float64(height - 2*padY)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="sparkline">`, width, height, width, height)

	if len(matrix.Samples) < 2 {
		// One or zero samples → flat (or no) baseline. Render an
		// empty SVG body so the grid layout stays intact.
		buf.WriteString(`</svg>`)
		return buf.Bytes()
	}

	min, _ := matrix.Min()
	max, _ := matrix.Max()
	span := max - min
	if span == 0 {
		// All samples equal — degenerate, render a flat midline.
		y := float64(height) / 2
		fmt.Fprintf(&buf, `<line x1="%d" y1="%.2f" x2="%d" y2="%.2f" stroke="currentColor" stroke-width="1" stroke-opacity="0.5"/>`,
			padX, y, width-padX, y)
		buf.WriteString(`</svg>`)
		return buf.Bytes()
	}

	// X-position is index-based (uniform spacing) — Prom range queries
	// already produce evenly-spaced samples at the step, so we don't
	// need to scale time-to-pixels separately.
	n := len(matrix.Samples)
	step := innerW / float64(n-1)

	yFor := func(v float64) float64 {
		// invert Y: SVG origin is top-left, we want larger values up
		return float64(padY) + innerH - ((v - min) / span * innerH)
	}

	// Build polyline points.
	buf.WriteString(`<polyline fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" stroke-linecap="round" points="`)
	for i, s := range matrix.Samples {
		if i > 0 {
			buf.WriteByte(' ')
		}
		x := float64(padX) + float64(i)*step
		fmt.Fprintf(&buf, "%.2f,%.2f", x, yFor(s.Value))
	}
	buf.WriteString(`"/>`)

	// Min + max ticks: short horizontal segments at the boundary
	// positions to anchor the eye. We mark the FIRST occurrence of
	// each extremum — for a flat-then-spike series this lines up
	// with where the operator visually expects the marker.
	minIdx, maxIdx := 0, 0
	for i, s := range matrix.Samples {
		if s.Value == min && minIdx == 0 {
			minIdx = i
		}
		if s.Value == max && maxIdx == 0 {
			maxIdx = i
		}
	}
	// Force unique-first via re-scan because the zero-init coincides
	// with index 0 when sample[0] isn't the extremum.
	minIdx = firstIndex(matrix.Samples, min)
	maxIdx = firstIndex(matrix.Samples, max)

	minX := float64(padX) + float64(minIdx)*step
	maxX := float64(padX) + float64(maxIdx)*step
	fmt.Fprintf(&buf, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="currentColor" stroke-width="2" stroke-opacity="0.6"/>`,
		minX-2, yFor(min), minX+2, yFor(min))
	fmt.Fprintf(&buf, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="currentColor" stroke-width="2" stroke-opacity="0.6"/>`,
		maxX-2, yFor(max), maxX+2, yFor(max))

	buf.WriteString(`</svg>`)
	return buf.Bytes()
}

func firstIndex(samples []PromSample, v float64) int {
	for i, s := range samples {
		if s.Value == v {
			return i
		}
	}
	return 0
}
