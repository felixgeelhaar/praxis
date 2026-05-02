package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenLoader_Disabled(t *testing.T) {
	tl, err := newTokenLoader("", "")
	if err != nil {
		t.Fatalf("newTokenLoader: %v", err)
	}
	if tl != nil {
		t.Errorf("expected nil loader when both static and path empty")
	}
	if tl.Token() != "" {
		t.Errorf("Token on nil loader=%q want empty", tl.Token())
	}
}

func TestTokenLoader_StaticOnly(t *testing.T) {
	tl, err := newTokenLoader("s3cret", "")
	if err != nil {
		t.Fatalf("newTokenLoader: %v", err)
	}
	if got := tl.Token(); got != "s3cret" {
		t.Errorf("Token=%q want s3cret", got)
	}
	// Reload of static is a no-op
	if err := tl.Reload(); err != nil {
		t.Errorf("Reload: %v", err)
	}
	if got := tl.Token(); got != "s3cret" {
		t.Errorf("post-reload Token=%q want s3cret", got)
	}
}

func TestTokenLoader_FileRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("v1\n"), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	tl, err := newTokenLoader("", path)
	if err != nil {
		t.Fatalf("newTokenLoader: %v", err)
	}
	if got := tl.Token(); got != "v1" {
		t.Errorf("Token=%q want v1", got)
	}
	if err := os.WriteFile(path, []byte("  v2  \n"), 0o600); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if got := tl.Token(); got != "v1" {
		t.Errorf("pre-Reload Token=%q want v1 (rotation should not auto-pick-up)", got)
	}
	if err := tl.Reload(); err != nil {
		t.Errorf("Reload: %v", err)
	}
	if got := tl.Token(); got != "v2" {
		t.Errorf("post-Reload Token=%q want v2", got)
	}
}

func TestTokenLoader_FileWinsOverStatic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("from-file"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tl, err := newTokenLoader("from-env", path)
	if err != nil {
		t.Fatalf("newTokenLoader: %v", err)
	}
	if got := tl.Token(); got != "from-file" {
		t.Errorf("Token=%q want from-file", got)
	}
}

func TestTokenLoader_EmptyFileRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := newTokenLoader("", path); err == nil {
		t.Error("expected error for whitespace-only token file")
	}
}

func TestTokenLoader_MissingFileRejected(t *testing.T) {
	if _, err := newTokenLoader("", "/nonexistent/token"); err == nil {
		t.Error("expected error for missing token file")
	}
}
