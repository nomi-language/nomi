package irbuild

import (
	"strings"
	"testing"
)

func TestSibAppField_NoInstalledAppIsRejectedBeforeGeneration(t *testing.T) {
	_, err := Analyze(fixture("appnostage/reader.nomi"))
	want := "`Settings.tag` reads an application field only when an entry boot returns `Settings`, and no `fn boot` in this project does"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected %q, got %v", want, err)
	}
}
