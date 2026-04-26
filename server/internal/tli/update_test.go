package tli

import (
	"strings"
	"testing"

	"boxland/server/internal/updater"
)

// updateBoxlandItem variants — the row should swap between
// "ready to apply" (HasUpdate=true) and "check only" (no opinion).

func TestUpdateBoxlandItem_ReadyToApply(t *testing.T) {
	it := updateBoxlandItem(&updater.Status{
		Current:   "0.1.0",
		Latest:    "v0.2.0",
		HasUpdate: true,
	})
	if it.title != updateBoxlandTitle {
		t.Fatalf("title = %q", it.title)
	}
	if it.badge != "ready" {
		t.Errorf("badge = %q, want ready", it.badge)
	}
	if !it.featured {
		t.Errorf("ready row should be featured")
	}
	if !it.interactive {
		t.Errorf("update needs TTY (git creds, etc.)")
	}
	if got := strings.Join(it.cmd, " "); got != "boxland update" {
		t.Errorf("cmd = %q, want `boxland update`", got)
	}
	if !strings.Contains(it.desc, "v0.2.0") {
		t.Errorf("desc should mention target version, got %q", it.desc)
	}
}

func TestUpdateBoxlandItem_CheckOnly(t *testing.T) {
	it := updateBoxlandItem(nil)
	if it.badge != "update" {
		t.Errorf("badge = %q, want update", it.badge)
	}
	if it.featured {
		t.Errorf("check-only row must not be featured")
	}
	if it.interactive {
		t.Errorf("--check is non-interactive")
	}
	if got := strings.Join(it.cmd, " "); got != "boxland update --check" {
		t.Errorf("cmd = %q", got)
	}
}

func TestUpdateBoxlandItem_UpToDateMentionsLatest(t *testing.T) {
	it := updateBoxlandItem(&updater.Status{
		Current:   "0.2.0",
		Latest:    "v0.2.0",
		HasUpdate: false,
	})
	if !strings.Contains(it.desc, "Up to date") {
		t.Errorf("desc should reassure user, got %q", it.desc)
	}
	if !strings.Contains(it.desc, "v0.2.0") {
		t.Errorf("desc should show what 'latest' is, got %q", it.desc)
	}
}

// itemsForState — when an update is available AND install is complete,
// Update Boxland pins to position 0 and Design slides to position 1.
// The "U" hotkey muscle memory must always find the row at a known
// title (updateBoxlandTitle), so position is the only thing changing.
func TestItemsForState_UpdatePinsTopWhenAvailable(t *testing.T) {
	upd := &updater.Status{Current: "0.1.0", Latest: "0.2.0", HasUpdate: true}
	items := itemsForState(true, upd)
	if items[0].title != updateBoxlandTitle {
		t.Errorf("position 0 = %q, want %q", items[0].title, updateBoxlandTitle)
	}
	if !items[0].featured {
		t.Errorf("update row at top must carry featured highlight")
	}
	if items[1].title != "Design" {
		t.Errorf("position 1 = %q, want Design", items[1].title)
	}
}

func TestItemsForState_UpdateAtBottomWhenUpToDate(t *testing.T) {
	items := itemsForState(true, nil)
	if items[0].title != "Design" {
		t.Errorf("position 0 = %q, want Design", items[0].title)
	}
	last := items[len(items)-1]
	if last.title != updateBoxlandTitle {
		t.Errorf("last item = %q, want %q (updates tucked at bottom when nothing new)",
			last.title, updateBoxlandTitle)
	}
}

func TestItemsForState_UpdateAlwaysPresent(t *testing.T) {
	for _, s := range []*updater.Status{
		nil,
		{HasUpdate: false, Latest: "0.1.0"},
		{HasUpdate: true, Latest: "0.2.0"},
	} {
		for _, complete := range []bool{true, false} {
			items := itemsForState(complete, s)
			found := false
			for _, it := range items {
				if it.title == updateBoxlandTitle {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("update row missing for complete=%v status=%+v",
					complete, s)
			}
		}
	}
}

// renderUpdateBanner — emits a one-line strip when an update exists,
// nothing otherwise. The banner must reference both versions so users
// know what they're moving from/to before they trigger it.
func TestRenderUpdateBanner_HiddenWithoutUpdate(t *testing.T) {
	m := newModel()
	m.applySize(120, 40)
	if got := m.renderUpdateBanner(); got != "" {
		t.Errorf("banner with nil status: %q", got)
	}
	m.updateStatus = &updater.Status{Current: "0.1.0", Latest: "0.1.0", HasUpdate: false}
	if got := m.renderUpdateBanner(); got != "" {
		t.Errorf("banner up-to-date: %q", got)
	}
}

func TestRenderUpdateBanner_ShownWithUpdate(t *testing.T) {
	m := newModel()
	m.applySize(120, 40)
	m.updateStatus = &updater.Status{Current: "0.1.0", Latest: "0.2.0", HasUpdate: true}
	got := m.renderUpdateBanner()
	if got == "" {
		t.Fatal("banner should render when HasUpdate=true")
	}
	if !strings.Contains(got, "v0.1.0") {
		t.Errorf("banner missing current version, got %q", got)
	}
	if !strings.Contains(got, "v0.2.0") {
		t.Errorf("banner missing target version, got %q", got)
	}
	if !strings.Contains(got, "Update available") {
		t.Errorf("banner missing label, got %q", got)
	}
	if !strings.Contains(got, "U") {
		t.Errorf("banner missing hotkey, got %q", got)
	}
}

// updateCheckMsg routes a fresh status into the model and rebuilds
// the menu so the row reflects the new state.
func TestUpdateCheckMsgUpdatesMenu(t *testing.T) {
	m := newModel()
	m.applySize(120, 40)
	// Without a status, the row is in check-only form.
	idx := itemIndex(m.list.Items(), updateBoxlandTitle)
	if idx < 0 {
		t.Fatal("update row should exist before any check")
	}
	if it := m.list.Items()[idx].(item); it.badge != "update" {
		t.Errorf("pre-check badge = %q, want update", it.badge)
	}

	m, _ = step(t, m, updateCheckMsg{status: &updater.Status{
		Current:   "0.1.0",
		Latest:    "0.2.0",
		HasUpdate: true,
	}})

	idx = itemIndex(m.list.Items(), updateBoxlandTitle)
	if idx < 0 {
		t.Fatal("update row missing after check")
	}
	it := m.list.Items()[idx].(item)
	if it.badge != "ready" {
		t.Errorf("post-check badge = %q, want ready", it.badge)
	}
	if !it.featured {
		t.Errorf("ready row must be featured to draw the eye")
	}
	if got := m.renderUpdateBanner(); got == "" {
		t.Errorf("banner should render after HasUpdate=true status")
	}
}

func TestUpdateCheckMsg_NilStatusKeepsExisting(t *testing.T) {
	m := newModel()
	m.applySize(120, 40)
	m.updateStatus = &updater.Status{Latest: "0.2.0", HasUpdate: true}
	m, _ = step(t, m, updateCheckMsg{status: nil})
	if m.updateStatus == nil {
		t.Errorf("nil incoming status should not wipe prior cached value")
	}
}


