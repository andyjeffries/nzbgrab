package yenc

import (
	"testing"
)

func TestDecodeLine(t *testing.T) {
	// yEnc encoding: output = (input + 42) mod 256
	// If output would be critical (NUL, LF, CR, '='), escape it:
	//   output = '=' followed by ((input + 42 + 64) mod 256)
	//
	// So to DECODE an escaped byte:
	//   input = (escaped_char - 42 - 64 + 256) mod 256
	//
	// The escape sequences and what they decode to:
	// =@ (64) -> (64 - 42 - 64 + 256) % 256 = 214 (0xD6)
	// =J (74) -> (74 - 42 - 64 + 256) % 256 = 224 (0xE0)
	// =M (77) -> (77 - 42 - 64 + 256) % 256 = 227 (0xE3)
	// =} (125) -> (125 - 42 - 64 + 256) % 256 = 19 (0x13)

	tests := []struct {
		encoded string
		want    []byte
	}{
		// Simple case: "J" decodes to space (32) - wait no
		// 'J' = 74, 74 - 42 = 32 (space) - YES
		{"J", []byte{' '}},
		// =@ decodes to 214 (0xD6)
		{"=@", []byte{0xD6}},
		// =J decodes to 224 (0xE0)
		{"=J", []byte{0xE0}},
		// =M decodes to 227 (0xE3)
		{"=M", []byte{0xE3}},
		// =} decodes to 19 (0x13)
		{"=}", []byte{0x13}},
	}

	for _, tt := range tests {
		got := decodeLine([]byte(tt.encoded))
		if string(got) != string(tt.want) {
			t.Errorf("decodeLine(%q) = %v, want %v", tt.encoded, got, tt.want)
		}
	}
}

func TestDecodeBytes(t *testing.T) {
	// To encode "HELLO":
	// H=72, 72+42=114='r'
	// E=69, 69+42=111='o'
	// L=76, 76+42=118='v'
	// L=76, 76+42=118='v'
	// O=79, 79+42=121='y'
	// So "HELLO" encodes to "rovvy"
	// CRC32 of "HELLO" = C1446436

	article := `=ybegin line=128 size=5 name=test.txt
rovvy
=yend size=5 crc32=C1446436
`

	result, err := DecodeBytes([]byte(article))
	if err != nil {
		t.Fatalf("DecodeBytes() error: %v", err)
	}

	if result.Header.Name != "test.txt" {
		t.Errorf("Name = %q, want %q", result.Header.Name, "test.txt")
	}
	if result.Header.Size != 5 {
		t.Errorf("Size = %d, want 5", result.Header.Size)
	}
	if string(result.Data) != "HELLO" {
		t.Errorf("Data = %q, want %q", string(result.Data), "HELLO")
	}
}

func TestDecodeMultipart(t *testing.T) {
	// Multipart article with =ypart
	// CRC32 of "HELLO" = C1446436
	article := `=ybegin part=1 total=2 line=128 size=10 name=test.txt
=ypart begin=1 end=5
rovvy
=yend size=5 part=1 pcrc32=C1446436
`

	result, err := DecodeBytes([]byte(article))
	if err != nil {
		t.Fatalf("DecodeBytes() error: %v", err)
	}

	if result.Header.Part != 1 {
		t.Errorf("Part = %d, want 1", result.Header.Part)
	}
	if result.Header.Total != 2 {
		t.Errorf("Total = %d, want 2", result.Header.Total)
	}
	if result.Header.Begin != 1 {
		t.Errorf("Begin = %d, want 1", result.Header.Begin)
	}
	if result.Header.End != 5 {
		t.Errorf("End = %d, want 5", result.Header.End)
	}
}

func TestParseYend(t *testing.T) {
	tests := []struct {
		line    string
		wantCRC uint32
		wantOK  bool
	}{
		{"=yend size=5 crc32=3610A686", 0x3610A686, true},
		{"=yend size=5 part=1 pcrc32=ABCD1234", 0xABCD1234, true},
		{"=yend size=5", 0, false},
	}

	for _, tt := range tests {
		gotCRC, gotOK := parseYend(tt.line)
		if gotCRC != tt.wantCRC || gotOK != tt.wantOK {
			t.Errorf("parseYend(%q) = (%X, %v), want (%X, %v)",
				tt.line, gotCRC, gotOK, tt.wantCRC, tt.wantOK)
		}
	}
}
