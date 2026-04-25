package persistence

import (
	"strings"
	"testing"
)

func TestContentAddressedKey_Deterministic(t *testing.T) {
	a := ContentAddressedKey("assets", []byte("hello world"))
	b := ContentAddressedKey("assets", []byte("hello world"))
	if a != b {
		t.Errorf("content-addressed key should be deterministic: %q vs %q", a, b)
	}
	c := ContentAddressedKey("assets", []byte("hello world!"))
	if a == c {
		t.Error("different bytes should produce different keys")
	}
}

func TestContentAddressedKey_LayoutShape(t *testing.T) {
	key := ContentAddressedKey("assets", []byte("anything"))
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		t.Fatalf("expected 4 path parts (prefix/aa/bb/sha), got %d in %q", len(parts), key)
	}
	if parts[0] != "assets" {
		t.Errorf("first part should be the prefix, got %q", parts[0])
	}
	if len(parts[1]) != 2 || len(parts[2]) != 2 {
		t.Errorf("expected 2-char hex shards, got %q / %q", parts[1], parts[2])
	}
	if len(parts[3]) != 64 {
		t.Errorf("sha256 hex should be 64 chars, got %d (%q)", len(parts[3]), parts[3])
	}
}

func TestPublicURL_TrailingSlash(t *testing.T) {
	o := &ObjectStore{publicBase: "https://cdn.example.com"}
	if got := o.PublicURL("assets/aa/bb/x"); got != "https://cdn.example.com/assets/aa/bb/x" {
		t.Errorf("got %q", got)
	}
	o2 := &ObjectStore{publicBase: "https://cdn.example.com"}
	if got := o2.PublicURL("/leading/slash/x"); got != "https://cdn.example.com/leading/slash/x" {
		t.Errorf("leading slash should not double up: %q", got)
	}
}

func TestPublicURL_EmptyBaseFallsBackToKey(t *testing.T) {
	o := &ObjectStore{publicBase: ""}
	if got := o.PublicURL("k"); got != "k" {
		t.Errorf("got %q", got)
	}
}
