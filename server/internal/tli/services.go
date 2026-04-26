// services.go — derive and detect the live HTTP services the TLI pins to
// the top of the logs pane while a Boxland server job is running.
//
// We don't ask the running subprocess "what URLs are you serving?" because
// the server doesn't print a banner. Instead we:
//
//  1. derive the URLs at TLI startup from BOXLAND_HTTP_ADDR (the same env
//     var server/internal/config consults; default ":8080"), and
//  2. only show them once we've seen the "http listening" log line that
//     runServe emits via slog. Until then the strip says "waiting…".
//
// Keeping detection a pure function makes it cheap to test without
// spawning a real server.

package tli

import (
	"os"
	"strings"
)

// ServiceLink is one row of the pinned-services strip in the logs pane.
type ServiceLink struct {
	Label string
	URL   string
}

// listeningMarker is the substring runServe writes to its log when the
// HTTP listener is up. We match on the raw line because slog's text
// handler emits "msg=\"http listening\"" and the JSON handler emits
// "\"msg\":\"http listening\"". The literal phrase is in both.
const listeningMarker = "http listening"

// DetectListening reports whether a captured stdout/stderr line indicates
// the Boxland HTTP server is now accepting connections.
func DetectListening(line string) bool {
	return strings.Contains(line, listeningMarker)
}

// ServiceLinks returns the URLs the TLI pins for an indefinite item that
// runs the Boxland HTTP server (today: "design" and "serve"). title is the
// item title; for items that don't run the server it returns nil so the
// caller can suppress the pinned strip entirely.
func ServiceLinks(itemTitle string) []ServiceLink {
	if !servesHTTP(itemTitle) {
		return nil
	}
	base := baseURL(os.Getenv("BOXLAND_HTTP_ADDR"))
	return []ServiceLink{
		{Label: "Design tools", URL: base + "/design/login"},
		{Label: "Game client", URL: base + "/play/login"},
		{Label: "Health check", URL: base + "/healthz"},
	}
}

// servesHTTP returns true for items whose subprocess will eventually bring
// up the boxland HTTP server.
func servesHTTP(itemTitle string) bool {
	switch itemTitle {
	case "Design", "Serve":
		return true
	}
	return false
}

// baseURL turns a BOXLAND_HTTP_ADDR-style listen string into a clickable
// http://… origin. We default to localhost:8080 (matching config.go) and
// substitute "localhost" for an empty host so ":8080" → "localhost:8080".
func baseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = ":8080"
	}
	host, port, ok := splitHostPort(addr)
	if !ok {
		// Fall back to treating the whole thing as a host (no port).
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + host + ":" + port
}

// splitHostPort is a tiny stand-in for net.SplitHostPort that tolerates
// the bare ":8080" form without pulling net/url validation into the TLI.
func splitHostPort(addr string) (host, port string, ok bool) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", false
	}
	return addr[:i], addr[i+1:], true
}
