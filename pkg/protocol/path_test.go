package protocol

import "testing"

func TestPathsOverlap(t *testing.T) {
	if !PathsOverlap("repositories/source", "repositories/source/docs") {
		t.Fatal("expected nested paths to overlap")
	}
	if PathsOverlap("repositories/source", "repositories/other") {
		t.Fatal("expected sibling paths not to overlap")
	}
}
