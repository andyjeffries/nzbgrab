package nzb

import (
	"testing"
)

func TestParse(t *testing.T) {
	nzb, err := Parse("../../testdata/small1.nzb")
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	if nzb.Name != "Hazel.Henry.Sparks.Fly.2026.RETAiL.EPUB" {
		t.Errorf("Name = %q, want %q", nzb.Name, "Hazel.Henry.Sparks.Fly.2026.RETAiL.EPUB")
	}

	if len(nzb.Files) != 8 {
		t.Errorf("len(Files) = %d, want 8", len(nzb.Files))
	}

	// Check total bytes
	total := nzb.TotalBytes()
	if total == 0 {
		t.Error("TotalBytes() = 0, want > 0")
	}
	t.Logf("Total bytes: %d", total)

	// Check file parsing
	var parCount, archiveCount int
	for _, f := range nzb.Files {
		if f.Filename == "" {
			t.Errorf("File has empty filename, subject: %s", f.Subject)
		}
		if f.IsPar2() {
			parCount++
		}
		if f.IsArchive() {
			archiveCount++
		}
		t.Logf("File: %s (%d bytes, %d segments, par2=%v, archive=%v)",
			f.Filename, f.Bytes, len(f.Segments), f.IsPar2(), f.IsArchive())
	}

	if parCount == 0 {
		t.Error("No PAR2 files detected")
	}
	if archiveCount == 0 {
		t.Error("No archive files detected")
	}
}

func TestExtractFilename(t *testing.T) {
	tests := []struct {
		subject string
		want    string
	}{
		{
			`[1/8] - "Hazel.Henry.Sparks.Fly.2026.RETAiL.EPUB.vol-01.par2" yEnc  40352 (1/1)`,
			"Hazel.Henry.Sparks.Fly.2026.RETAiL.EPUB.vol-01.par2",
		},
		{
			`[2/8] - "Hazel.Henry.Sparks.Fly.2026.RETAiL.EPUB.part01.rar" yEnc  2258670 (1/4)`,
			"Hazel.Henry.Sparks.Fly.2026.RETAiL.EPUB.part01.rar",
		},
		{
			`No quotes here`,
			"",
		},
	}

	for _, tt := range tests {
		got := extractFilename(tt.subject)
		if got != tt.want {
			t.Errorf("extractFilename(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

func TestFileMethods(t *testing.T) {
	tests := []struct {
		filename  string
		isPar2    bool
		isArchive bool
	}{
		{"file.par2", true, false},
		{"file.PAR2", true, false},
		{"file.vol01+02.par2", true, false},
		{"file.rar", false, true},
		{"file.RAR", false, true},
		{"file.r00", false, true},
		{"file.r99", false, true},
		{"file.zip", false, true},
		{"file.7z", false, true},
		{"file.txt", false, false},
		{"file.mkv", false, false},
	}

	for _, tt := range tests {
		f := &File{Filename: tt.filename}
		if f.IsPar2() != tt.isPar2 {
			t.Errorf("%s: IsPar2() = %v, want %v", tt.filename, f.IsPar2(), tt.isPar2)
		}
		if f.IsArchive() != tt.isArchive {
			t.Errorf("%s: IsArchive() = %v, want %v", tt.filename, f.IsArchive(), tt.isArchive)
		}
	}
}
