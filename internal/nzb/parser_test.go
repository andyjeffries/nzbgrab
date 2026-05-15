package nzb

import (
	"testing"
)

// Sample NZB XML for testing
const testNZB = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE nzb PUBLIC "-//newzBin//DTD NZB 1.1//EN" "http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd">
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <head>
    <meta type="name">Test.Release.2024</meta>
  </head>
  <file poster="test@example.com" date="1234567890" subject="[1/3] - &quot;test.vol00+01.par2&quot; yEnc (1/1)">
    <groups><group>alt.binaries.test</group></groups>
    <segments>
      <segment bytes="50000" number="1">abc123@example.com</segment>
    </segments>
  </file>
  <file poster="test@example.com" date="1234567890" subject="[2/3] - &quot;test.rar&quot; yEnc (1/2)">
    <groups><group>alt.binaries.test</group></groups>
    <segments>
      <segment bytes="768000" number="1">def456@example.com</segment>
      <segment bytes="768000" number="2">ghi789@example.com</segment>
    </segments>
  </file>
  <file poster="test@example.com" date="1234567890" subject="[3/3] - &quot;test.mkv&quot; yEnc (1/1)">
    <groups><group>alt.binaries.test</group></groups>
    <segments>
      <segment bytes="1000000" number="1">jkl012@example.com</segment>
    </segments>
  </file>
</nzb>`

func TestParseBytes(t *testing.T) {
	nzb, err := ParseBytes([]byte(testNZB), "test.nzb")
	if err != nil {
		t.Fatalf("ParseBytes() error: %v", err)
	}

	if nzb.Name != "Test.Release.2024" {
		t.Errorf("Name = %q, want %q", nzb.Name, "Test.Release.2024")
	}

	if len(nzb.Files) != 3 {
		t.Errorf("len(Files) = %d, want 3", len(nzb.Files))
	}

	// Check total bytes
	total := nzb.TotalBytes()
	expected := int64(50000 + 768000 + 768000 + 1000000)
	if total != expected {
		t.Errorf("TotalBytes() = %d, want %d", total, expected)
	}

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
	}

	if parCount != 1 {
		t.Errorf("PAR2 count = %d, want 1", parCount)
	}
	if archiveCount != 1 {
		t.Errorf("Archive count = %d, want 1", archiveCount)
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
