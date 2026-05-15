// Package yenc implements yEnc decoding for Usenet binary articles.
//
// yEnc is an 8-bit encoding where each byte is encoded as (original + 42) mod 256.
// Only a few bytes are escaped with '=' prefix: NUL (0x00), LF (0x0A), CR (0x0D),
// '=' (0x3D), and optionally TAB (0x09) and space (0x20) at line boundaries.
package yenc

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
)

// Header contains parsed yEnc header information.
type Header struct {
	Name  string // Filename
	Line  int    // Line length
	Size  int64  // Total file size
	Part  int    // Part number (for multipart)
	Total int    // Total parts (for multipart)
	Begin int64  // Byte offset start (for =ypart)
	End   int64  // Byte offset end (for =ypart)
}

// Result contains the decoded data and metadata.
type Result struct {
	Header Header
	Data   []byte
	CRC32  uint32 // Calculated CRC32
}

// Decode decodes yEnc encoded data from a reader.
// The reader should contain the article body (after NNTP headers).
func Decode(r io.Reader) (*Result, error) {
	scanner := bufio.NewScanner(r)
	result := &Result{}

	// Find and parse =ybegin line
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "=ybegin ") {
			if err := parseYbegin(line, &result.Header); err != nil {
				return nil, fmt.Errorf("parsing =ybegin: %w", err)
			}
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning for =ybegin: %w", err)
	}

	// Check for =ypart line (multipart)
	if scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "=ypart ") {
			if err := parseYpart(line, &result.Header); err != nil {
				return nil, fmt.Errorf("parsing =ypart: %w", err)
			}
		} else {
			// Not a ypart line, decode it
			decoded := decodeLine([]byte(line))
			result.Data = append(result.Data, decoded...)
		}
	}

	// Decode body until =yend
	var expectedCRC uint32
	var haveCRC bool

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "=yend ") {
			// Parse =yend for CRC
			expectedCRC, haveCRC = parseYend(line)
			break
		}

		decoded := decodeLine([]byte(line))
		result.Data = append(result.Data, decoded...)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning body: %w", err)
	}

	// Calculate CRC32
	result.CRC32 = crc32.ChecksumIEEE(result.Data)

	// Verify CRC if present
	if haveCRC && result.CRC32 != expectedCRC {
		return result, fmt.Errorf("CRC32 mismatch: got %08X, expected %08X", result.CRC32, expectedCRC)
	}

	return result, nil
}

// DecodeBytes is a convenience function to decode from a byte slice.
func DecodeBytes(data []byte) (*Result, error) {
	return Decode(bytes.NewReader(data))
}

// parseYbegin parses the =ybegin header line.
// Format: =ybegin part=1 total=10 line=128 size=500000 name=file.rar
func parseYbegin(line string, h *Header) error {
	parts := strings.Fields(line)
	for _, part := range parts[1:] { // Skip "=ybegin"
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := kv[0], kv[1]

		switch key {
		case "name":
			// Name may contain spaces, so we need to get the rest of the line
			idx := strings.Index(line, "name=")
			if idx >= 0 {
				h.Name = line[idx+5:]
			}
			return nil // Name is always last, so we're done
		case "line":
			if n, err := strconv.Atoi(val); err == nil {
				h.Line = n
			}
		case "size":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				h.Size = n
			}
		case "part":
			if n, err := strconv.Atoi(val); err == nil {
				h.Part = n
			}
		case "total":
			if n, err := strconv.Atoi(val); err == nil {
				h.Total = n
			}
		}
	}
	return nil
}

// parseYpart parses the =ypart header line.
// Format: =ypart begin=1 end=50000
func parseYpart(line string, h *Header) error {
	parts := strings.Fields(line)
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := kv[0], kv[1]

		switch key {
		case "begin":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				h.Begin = n
			}
		case "end":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil {
				h.End = n
			}
		}
	}
	return nil
}

// parseYend parses the =yend trailer line for CRC32.
// Format: =yend size=50000 part=1 pcrc32=ABCD1234
// Returns the CRC and whether it was found.
func parseYend(line string) (uint32, bool) {
	// Look for pcrc32= (part CRC) or crc32= (whole file CRC)
	for _, prefix := range []string{"pcrc32=", "crc32="} {
		idx := strings.Index(line, prefix)
		if idx >= 0 {
			hexStr := line[idx+len(prefix):]
			// Extract just the hex value (stop at space or end)
			if spaceIdx := strings.Index(hexStr, " "); spaceIdx >= 0 {
				hexStr = hexStr[:spaceIdx]
			}
			if val, err := strconv.ParseUint(hexStr, 16, 32); err == nil {
				return uint32(val), true
			}
		}
	}
	return 0, false
}

// decodeLine decodes a single line of yEnc data.
// yEnc encoding: output = (input + 42) mod 256
// Escape encoding: for critical bytes, output = '=' followed by (input + 42 + 64) mod 256
// So decoding: normal byte = (input - 42) mod 256
//              escaped byte = (input - 64 - 42) mod 256 = (input - 106) mod 256
func decodeLine(line []byte) []byte {
	result := make([]byte, 0, len(line))

	for i := 0; i < len(line); i++ {
		b := line[i]

		if b == '=' && i+1 < len(line) {
			// Escape sequence: the encoded critical byte had 64 added before the +42
			// So to decode: (char - 42 - 64) mod 256
			i++
			b = line[i] - 42 - 64
		} else {
			// Normal byte: (char - 42) mod 256
			b = b - 42
		}

		result = append(result, b)
	}

	return result
}
