package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// Badge colors — matched to the dashboard's own palette (the same
// emerald/amber/red used for stat tones and status badges there) rather
// than shields.io's default neon greens, so a badge someone embeds in
// their README actually looks like it belongs to Sentry Load.
const (
	badgeColorLabel              = "#5b4fd6" // close to the dashboard's --primary
	badgeColorGreen              = "#059669"
	badgeColorAmber              = "#d97706"
	badgeColorRed                = "#dc2626"
	badgeColorGray               = "#6b7280"
	badgeErrorRateAmberThreshold = 5.0 // percent
)

// handleBadge serves an embeddable SVG badge for a shared test — no auth,
// same as the report itself (M11/M15). Always returns a valid badge, even
// for an unknown token or a misconfigured server, rather than an error
// response: this is meant to be dropped straight into an <img src="...">,
// and a broken-image icon in someone's README is a worse failure mode
// than a gray "not found" badge.
func (s *apiServer) handleBadge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache")

	if s.history == nil {
		writeBadge(w, "sentry load", "unavailable", badgeColorGray)
		return
	}

	token := strings.TrimSuffix(r.PathValue("token"), ".svg")
	snap, ok, err := s.history.GetByShareToken(r.Context(), token)
	if err != nil {
		log.Printf("failed to load badge for %s: %v", token, err)
		writeBadge(w, "sentry load", "error", badgeColorGray)
		return
	}
	if !ok {
		writeBadge(w, "sentry load", "not found", badgeColorGray)
		return
	}

	errorRate := 0.0
	if snap.TotalRequests > 0 {
		errorRate = float64(snap.TotalErrors) / float64(snap.TotalRequests) * 100
	}

	color := badgeColorGreen
	switch {
	case snap.CircuitBroken:
		color = badgeColorRed
	case errorRate > badgeErrorRateAmberThreshold:
		color = badgeColorAmber
	}

	message := fmt.Sprintf("%.0f rps · %.1f%% err", snap.CombinedRPS, errorRate)
	writeBadge(w, "load tested", message, color)
}

// writeBadge renders a minimal two-segment badge (label | message) with
// rounded outer corners — hand-rolled rather than a dependency, same
// reasoning as the dashboard's LineChart/BarChart: the output is simple
// enough that it doesn't need one. Text width is estimated from character
// count rather than real font metrics, which won't be pixel-perfect but is
// good enough for a small embeddable badge.
//
// Rounded corners on an SVG made of two adjacent rects need a clip path —
// rounding each rect individually would round all four of its corners,
// not just the outer two — so the two flat-cornered segments are drawn
// inside a group clipped to one rounded-rect shape.
func writeBadge(w http.ResponseWriter, label, message, color string) {
	const charWidth = 6.5
	const padding = 10
	labelWidth := int(float64(len(label))*charWidth) + padding
	messageWidth := int(float64(len(message))*charWidth) + padding
	totalWidth := labelWidth + messageWidth

	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
  <clipPath id="rounded">
    <rect width="%d" height="20" rx="4" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#rounded)">
    <rect width="%d" height="20" fill="%s"/>
    <rect x="%d" width="%d" height="20" fill="%s"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,sans-serif" font-size="11">
    <text x="%d" y="14">%s</text>
    <text x="%d" y="14">%s</text>
  </g>
</svg>`,
		totalWidth, label, message,
		totalWidth,
		labelWidth, badgeColorLabel,
		labelWidth, messageWidth, color,
		labelWidth/2, label,
		labelWidth+messageWidth/2, message,
	)
}
