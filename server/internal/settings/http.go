package settings

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// HTTPHandlers wires the GET + PUT /settings/me endpoints used by both
// realm packages. Each realm passes a Resolver that pulls (realm,
// subject_id) off the request -- typically by reading the auth context
// the realm's LoadSession middleware populated.
type HTTPHandlers struct {
	Service  *Service
	Resolver func(*http.Request) (Realm, int64, bool)
}

// MaxPayloadBytes caps a single PUT body. 64 KiB is comfortably more
// than enough for the v1 settings shape (font, audio levels, ~50
// hotkey rebindings); the limit exists to stop abuse.
const MaxPayloadBytes = 64 * 1024

// Get handles GET /settings/me. Returns 401 when the resolver can't
// pin a subject; 200 with `{}` when the row hasn't been written yet
// (first-time login).
func (h *HTTPHandlers) Get(w http.ResponseWriter, r *http.Request) {
	realm, subjectID, ok := h.Resolver(r)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	payload, err := h.Service.Get(r.Context(), realm, subjectID)
	if err != nil {
		slog.Warn("settings get", "realm", realm, "subject", subjectID, "err", err)
		http.Error(w, "settings: get failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(payload)
}

// Put handles PUT /settings/me. Body MUST be a JSON object; the service
// validates and persists the entire blob (the client is canonical for
// shape, the server only stores).
func (h *HTTPHandlers) Put(w http.ResponseWriter, r *http.Request) {
	realm, subjectID, ok := h.Resolver(r)
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxPayloadBytes+1))
	if err != nil {
		http.Error(w, "settings: read body", http.StatusBadRequest)
		return
	}
	if len(body) > MaxPayloadBytes {
		http.Error(w, "settings: payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	// Validate decode roundtrips so a malformed POST gives a useful 400
	// rather than a 500 from the service-side check.
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		http.Error(w, "settings: invalid JSON object", http.StatusBadRequest)
		return
	}
	if err := h.Service.Save(r.Context(), realm, subjectID, body); err != nil {
		slog.Warn("settings save", "realm", realm, "subject", subjectID, "err", err)
		http.Error(w, "settings: save failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
