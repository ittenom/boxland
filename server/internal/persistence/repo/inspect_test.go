package repo

import (
	"reflect"
	"testing"
)

type good struct {
	ID        int64  `db:"id"        pk:"auto"`
	Name      string `db:"name"`
	CreatedAt string `db:"created_at" repo:"readonly"`
	Skipped   string // no db tag → ignored
	private   int    `db:"private"`
}

type missingPK struct {
	Name string `db:"name"`
}

type doublePK struct {
	A int `db:"a" pk:"auto"`
	B int `db:"b" pk:"auto"`
}

func TestInspect_Good(t *testing.T) {
	info, err := inspect(reflect.TypeOf(good{}))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if info.pkColumn != "id" {
		t.Errorf("pk column: got %q", info.pkColumn)
	}
	wantAll := []string{"id", "name", "created_at"}
	if !equalSlice(info.allColumns, wantAll) {
		t.Errorf("allColumns: got %v, want %v", info.allColumns, wantAll)
	}
	// INSERT excludes auto-pk and readonly
	if !equalSlice(info.insertColumns, []string{"name"}) {
		t.Errorf("insertColumns: got %v", info.insertColumns)
	}
	// UPDATE excludes pk and readonly
	if !equalSlice(info.updateColumns, []string{"name"}) {
		t.Errorf("updateColumns: got %v", info.updateColumns)
	}
}

func TestInspect_MissingPKPanics(t *testing.T) {
	_, err := inspect(reflect.TypeOf(missingPK{}))
	if err == nil {
		t.Fatal("expected error for missing pk")
	}
}

func TestInspect_DoublePKErrors(t *testing.T) {
	_, err := inspect(reflect.TypeOf(doublePK{}))
	if err == nil {
		t.Fatal("expected error for two pk fields")
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
