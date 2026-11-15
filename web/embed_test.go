package web

import (
	"bytes"
	"io/fs"
	"testing"
)

func TestAssetsContainsBuiltApplication(t *testing.T) {
	assets := Assets()
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(index, []byte("Posthaste")) {
		t.Fatal("compiled index is missing the application title")
	}
	files, err := fs.Glob(assets, "assets/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("compiled JavaScript assets not found: %v", err)
	}
}
