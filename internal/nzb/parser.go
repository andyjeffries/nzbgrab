// Package nzb handles parsing NZB XML files.
package nzb

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// NZB represents a parsed NZB file.
type NZB struct {
	Name     string            // Extracted from metadata or filename
	Meta     map[string]string // Metadata key-value pairs
	Files    []*File           // Files to download
	Filename string            // Original NZB filename (basename)
	Path     string            // Full path to the NZB file
}

// File represents a single file within an NZB.
type File struct {
	Poster   string     // Who posted the file
	Date     int64      // Unix timestamp
	Subject  string     // Article subject
	Groups   []string   // Newsgroups
	Segments []*Segment // Article segments
	Filename string     // Extracted filename from subject
	Bytes    int64      // Total size in bytes
}

// Segment represents a single article segment.
type Segment struct {
	Bytes     int64  // Size in bytes
	Number    int    // Part number (1-indexed)
	MessageID string // Article Message-ID
}

// TotalBytes returns the total size of all files in the NZB.
func (n *NZB) TotalBytes() int64 {
	var total int64
	for _, f := range n.Files {
		total += f.Bytes
	}
	return total
}

// nzbXML is the raw XML structure for unmarshaling.
type nzbXML struct {
	XMLName xml.Name  `xml:"nzb"`
	Head    headXML   `xml:"head"`
	Files   []fileXML `xml:"file"`
}

type headXML struct {
	Meta []metaXML `xml:"meta"`
}

type metaXML struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type fileXML struct {
	Poster   string      `xml:"poster,attr"`
	Date     string      `xml:"date,attr"`
	Subject  string      `xml:"subject,attr"`
	Groups   groupsXML   `xml:"groups"`
	Segments segmentsXML `xml:"segments"`
}

type groupsXML struct {
	Groups []string `xml:"group"`
}

type segmentsXML struct {
	Segments []segmentXML `xml:"segment"`
}

type segmentXML struct {
	Bytes     string `xml:"bytes,attr"`
	Number    string `xml:"number,attr"`
	MessageID string `xml:",chardata"`
}

// Parse reads and parses an NZB file.
func Parse(path string) (*NZB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading nzb file: %w", err)
	}

	nzb, err := ParseBytes(data, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	nzb.Path = path
	return nzb, nil
}

// ParseBytes parses NZB content from bytes.
func ParseBytes(data []byte, filename string) (*NZB, error) {
	var raw nzbXML
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing nzb xml: %w", err)
	}

	nzb := &NZB{
		Meta:     make(map[string]string),
		Filename: filename,
	}

	// Parse metadata
	for _, m := range raw.Head.Meta {
		nzb.Meta[m.Type] = m.Value
	}

	// Extract name from metadata or filename
	if name, ok := nzb.Meta["name"]; ok {
		nzb.Name = name
	} else if name, ok := nzb.Meta["title"]; ok {
		nzb.Name = name
	} else {
		nzb.Name = strings.TrimSuffix(filename, ".nzb")
	}

	// Parse files
	for _, f := range raw.Files {
		file := &File{
			Poster:  f.Poster,
			Subject: f.Subject,
			Groups:  f.Groups.Groups,
		}

		// Parse date
		if d, err := strconv.ParseInt(f.Date, 10, 64); err == nil {
			file.Date = d
		}

		// Extract filename from subject
		file.Filename = extractFilename(f.Subject)

		// Parse segments
		for _, s := range f.Segments.Segments {
			seg := &Segment{
				MessageID: strings.TrimSpace(s.MessageID),
			}

			if b, err := strconv.ParseInt(s.Bytes, 10, 64); err == nil {
				seg.Bytes = b
				file.Bytes += b
			}

			if n, err := strconv.Atoi(s.Number); err == nil {
				seg.Number = n
			}

			file.Segments = append(file.Segments, seg)
		}

		// Sort segments by number
		sort.Slice(file.Segments, func(i, j int) bool {
			return file.Segments[i].Number < file.Segments[j].Number
		})

		nzb.Files = append(nzb.Files, file)
	}

	return nzb, nil
}

// extractFilename pulls the filename from a subject line.
// Subject format is typically: [1/8] - "filename.ext" yEnc (1/1)
var filenameRe = regexp.MustCompile(`"([^"]+)"`)

func extractFilename(subject string) string {
	matches := filenameRe.FindStringSubmatch(subject)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// IsPar2 returns true if the file is a PAR2 file.
func (f *File) IsPar2() bool {
	lower := strings.ToLower(f.Filename)
	return strings.HasSuffix(lower, ".par2")
}

// IsArchive returns true if the file is an archive (rar, zip, 7z).
func (f *File) IsArchive() bool {
	lower := strings.ToLower(f.Filename)
	return strings.HasSuffix(lower, ".rar") ||
		strings.HasSuffix(lower, ".zip") ||
		strings.HasSuffix(lower, ".7z") ||
		// Multi-part rar files: .r00, .r01, etc.
		regexp.MustCompile(`\.r\d{2}$`).MatchString(lower)
}
