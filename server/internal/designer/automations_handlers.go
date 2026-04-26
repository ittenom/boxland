package designer

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"boxland/server/internal/automations"
	"boxland/server/internal/configurable"
	"boxland/server/views"
)

// postAutomationAdd appends a new Automation to the entity-type's
// AutomationSet. The form submits a trigger kind; we attach a default
// no-op action so the AST validates immediately (an automation needs at
// least one action). Designers add real actions via the per-action
// editor.
func postAutomationAdd(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if d.Automations == nil {
			http.Error(w, "automations service not configured", http.StatusInternalServerError)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		triggerKind := strings.TrimSpace(r.FormValue("trigger_kind"))
		if triggerKind == "" {
			http.Error(w, "missing trigger_kind", http.StatusBadRequest)
			return
		}
		tdef, ok := d.AutomationTriggers.Get(triggerKind)
		if !ok {
			http.Error(w, "unknown trigger kind: "+triggerKind, http.StatusBadRequest)
			return
		}

		set, err := d.Automations.Get(r.Context(), entityID)
		if err != nil {
			slog.Error("automations get", "err", err, "entity_id", entityID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		// Seed: default trigger config + a single placeholder action so
		// the AST passes Validate (which requires at least one action).
		// We use ActionDespawn with target_self=false as the cheapest
		// no-op; designers swap it for a real action immediately.
		triggerCfg, _ := json.Marshal(tdef.Default())
		actionDef, _ := d.AutomationActions.Get("despawn")
		actionCfg, _ := json.Marshal(actionDef.Default())

		set.Automations = append(set.Automations, automations.Automation{
			Name:    triggerKind,
			Trigger: automations.TriggerNode{Kind: triggerKind, Config: triggerCfg},
			Actions: []automations.ActionNode{
				{Kind: "despawn", Config: actionCfg},
			},
		})

		if err := d.Automations.Save(r.Context(), entityID, set); err != nil {
			slog.Error("automations save (add)", "err", err, "entity_id", entityID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		renderAutomationsBlock(w, r, d, entityID)
	}
}

// postAutomationSave persists changes to an automation's trigger config.
// The form encodes one node's fields; we patch only that node.
func postAutomationSave(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx, err := strconv.Atoi(r.PathValue("idx"))
		if err != nil || idx < 0 {
			http.Error(w, "bad idx", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		set, err := d.Automations.Get(r.Context(), entityID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if idx >= len(set.Automations) {
			http.Error(w, "automation idx out of range", http.StatusBadRequest)
			return
		}

		// Optional: rename
		if name := strings.TrimSpace(r.FormValue("__name")); name != "" {
			set.Automations[idx].Name = name
		}
		// Decode trigger config from form fields prefixed by trigger.
		auto := &set.Automations[idx]
		tdef, ok := d.AutomationTriggers.Get(auto.Trigger.Kind)
		if !ok {
			http.Error(w, "unknown trigger kind", http.StatusBadRequest)
			return
		}
		cfg, err := decodeFormBlob(r.Form, "trigger.", tdef.Descriptor())
		if err != nil {
			http.Error(w, "bad trigger config: "+err.Error(), http.StatusBadRequest)
			return
		}
		auto.Trigger.Config = cfg

		if err := d.Automations.Save(r.Context(), entityID, set); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusBadRequest)
			return
		}
		renderAutomationsBlock(w, r, d, entityID)
	}
}

// deleteAutomation removes an automation by index.
func deleteAutomation(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx, err := strconv.Atoi(r.PathValue("idx"))
		if err != nil || idx < 0 {
			http.Error(w, "bad idx", http.StatusBadRequest)
			return
		}
		set, err := d.Automations.Get(r.Context(), entityID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if idx >= len(set.Automations) {
			http.Error(w, "out of range", http.StatusBadRequest)
			return
		}
		set.Automations = append(set.Automations[:idx], set.Automations[idx+1:]...)
		if err := d.Automations.Save(r.Context(), entityID, set); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}
		renderAutomationsBlock(w, r, d, entityID)
	}
}

// postAutomationActionAdd appends a new action onto an automation.
func postAutomationActionAdd(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx, err := strconv.Atoi(r.PathValue("idx"))
		if err != nil || idx < 0 {
			http.Error(w, "bad idx", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		actionKind := strings.TrimSpace(r.FormValue("action_kind"))
		if actionKind == "" {
			http.Error(w, "missing action_kind", http.StatusBadRequest)
			return
		}
		adef, ok := d.AutomationActions.Get(actionKind)
		if !ok {
			http.Error(w, "unknown action kind: "+actionKind, http.StatusBadRequest)
			return
		}

		set, err := d.Automations.Get(r.Context(), entityID)
		if err != nil || idx >= len(set.Automations) {
			http.Error(w, "automation not found", http.StatusBadRequest)
			return
		}
		cfg, _ := json.Marshal(adef.Default())
		set.Automations[idx].Actions = append(set.Automations[idx].Actions, automations.ActionNode{
			Kind: actionKind, Config: cfg,
		})
		if err := d.Automations.Save(r.Context(), entityID, set); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusBadRequest)
			return
		}
		renderAutomationsBlock(w, r, d, entityID)
	}
}

// deleteAutomationAction removes one action from an automation. If it
// was the last action, we re-seed a despawn placeholder (matching what
// postAutomationAdd uses) so the AST stays valid AND the user's
// trigger configuration survives. The previous behavior — silently
// dropping the entire automation — was a foot-gun: clicking × on the
// only visible action wiped the trigger's config without warning.
func deleteAutomationAction(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID, err := pathID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		idx, err := strconv.Atoi(r.PathValue("idx"))
		if err != nil || idx < 0 {
			http.Error(w, "bad idx", http.StatusBadRequest)
			return
		}
		aIdx, err := strconv.Atoi(r.PathValue("aIdx"))
		if err != nil || aIdx < 0 {
			http.Error(w, "bad aIdx", http.StatusBadRequest)
			return
		}
		set, err := d.Automations.Get(r.Context(), entityID)
		if err != nil || idx >= len(set.Automations) {
			http.Error(w, "automation not found", http.StatusBadRequest)
			return
		}
		auto := &set.Automations[idx]
		if aIdx >= len(auto.Actions) {
			http.Error(w, "action idx out of range", http.StatusBadRequest)
			return
		}
		auto.Actions = append(auto.Actions[:aIdx], auto.Actions[aIdx+1:]...)
		if len(auto.Actions) == 0 {
			// Removing the last action would invalidate the AST. Re-seed
			// a despawn placeholder so the trigger + its config survive;
			// designer swaps the placeholder for a real action without
			// losing setup. Mirrors postAutomationAdd's seed.
			if d.AutomationActions != nil {
				if adef, ok := d.AutomationActions.Get("despawn"); ok {
					cfg, _ := json.Marshal(adef.Default())
					auto.Actions = []automations.ActionNode{
						{Kind: "despawn", Config: cfg},
					}
				}
			}
		}
		if err := d.Automations.Save(r.Context(), entityID, set); err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusBadRequest)
			return
		}
		renderAutomationsBlock(w, r, d, entityID)
	}
}

// renderAutomationsBlock re-renders the entire automations block as
// an HTMX outerHTML swap. Cheaper than reloading the whole entity
// detail; the block has all the state it needs.
func renderAutomationsBlock(w http.ResponseWriter, r *http.Request, d Deps, entityID int64) {
	set, err := d.Automations.Get(r.Context(), entityID)
	if err != nil {
		slog.Error("automations get (render)", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	props := views.AutomationsBlockProps{
		EntityTypeID: entityID,
		Set:          set,
		Triggers:     d.AutomationTriggers,
		Actions:      d.AutomationActions,
	}
	renderHTML(w, r, views.AutomationsBlock(props))
}

// decodeFormBlob converts a flat HTTP form into a JSON config blob using
// a FieldDescriptor list. Each descriptor's Key is matched against
// "<prefix><Key>" in the form. Numeric / bool / color fields are
// coerced; missing fields are omitted (callers fall back to defaults).
//
// The renderer side already encodes nested + list fields as
// "<prefix><parent>.<child>" / "<prefix><parent>[N].<child>"; for v1
// the automation editor only uses flat scalar fields, so we keep this
// helper simple and rely on the existing Form templ for components.
func decodeFormBlob(form map[string][]string, prefix string, fields []configurable.FieldDescriptor) (json.RawMessage, error) {
	out := map[string]any{}
	for _, f := range fields {
		raw, ok := form[prefix+f.Key]
		if !ok || len(raw) == 0 {
			continue
		}
		v := strings.TrimSpace(raw[0])
		if v == "" {
			continue
		}
		switch f.Kind {
		case configurable.KindInt, configurable.KindEntityTypeRef, configurable.KindAssetRef:
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, errors.New(f.Key + ": expected integer")
			}
			out[f.Key] = n
		case configurable.KindFloat:
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, errors.New(f.Key + ": expected number")
			}
			out[f.Key] = n
		case configurable.KindBool:
			out[f.Key] = (v == "true" || v == "on" || v == "1")
		case configurable.KindColor:
			// Color fields ship as #rrggbb from <input type="color"> —
			// reuse parseHexColor to get the 0xRRGGBBAA encoding.
			out[f.Key] = parseHexColor(v)
		default:
			out[f.Key] = v
		}
	}
	return json.Marshal(out)
}
