package version

import "testing"

func TestDefaultExportArchiveUsesLabel(t *testing.T) {
	original := Build
	Build = "v0.2.0 test"
	defer func() { Build = original }()

	got := DefaultExportArchive()
	want := "gemini-export-v0.2.0-test.zip"
	if got != want {
		t.Fatalf("DefaultExportArchive() = %q, want %q", got, want)
	}
}
