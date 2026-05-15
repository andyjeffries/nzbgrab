// Package extract provides archive extraction functionality.
package extract

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Command timeout for extraction
const cmdTimeout = 30 * time.Minute

// obfuscatedPattern matches filenames that are just alphanumeric with no meaningful separators
var obfuscatedPattern = regexp.MustCompile(`^[a-zA-Z0-9]{10,}$`)

// Result contains the result of extraction.
type Result struct {
	Extracted    bool     // True if extraction was successful
	Files        []string // List of extracted files
	Message      string   // Status message
	ArchiveFiles []string // Archive files that were extracted (for cleanup)
}

// ArchiveType represents the type of archive.
type ArchiveType int

const (
	ArchiveUnknown ArchiveType = iota
	ArchiveRAR
	ArchiveZIP
	Archive7Z
)

// Extract extracts archives in the given directory.
// It automatically detects the archive type and uses the appropriate tool.
func Extract(dir string) (*Result, error) {
	archives, archiveType := findArchives(dir)
	if len(archives) == 0 {
		return &Result{
			Extracted: false,
			Message:   "no archives found",
		}, nil
	}

	switch archiveType {
	case ArchiveRAR:
		return extractRAR(dir, archives)
	case ArchiveZIP:
		return extractZIP(dir, archives)
	case Archive7Z:
		return extract7Z(dir, archives)
	default:
		return &Result{
			Message: "unknown archive type",
		}, nil
	}
}

// Available checks which extraction tools are available.
func Available() map[string]bool {
	tools := make(map[string]bool)
	for _, tool := range []string{"unrar", "7z", "unzip"} {
		_, err := exec.LookPath(tool)
		tools[tool] = err == nil
	}
	return tools
}

// findArchives finds archive files in a directory and determines the type.
func findArchives(dir string) ([]string, ArchiveType) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ArchiveUnknown
	}

	var rarFiles, zipFiles, sevenZFiles []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		fullPath := filepath.Join(dir, entry.Name())

		if strings.HasSuffix(name, ".rar") || isRARPart(name) {
			rarFiles = append(rarFiles, fullPath)
		} else if strings.HasSuffix(name, ".zip") {
			zipFiles = append(zipFiles, fullPath)
		} else if strings.HasSuffix(name, ".7z") {
			sevenZFiles = append(sevenZFiles, fullPath)
		}
	}

	// Return the first non-empty type found
	if len(rarFiles) > 0 {
		return rarFiles, ArchiveRAR
	}
	if len(zipFiles) > 0 {
		return zipFiles, ArchiveZIP
	}
	if len(sevenZFiles) > 0 {
		return sevenZFiles, Archive7Z
	}

	return nil, ArchiveUnknown
}

// isRARPart checks if a filename is a RAR multi-part file (.r00, .r01, etc.)
func isRARPart(name string) bool {
	if len(name) < 4 {
		return false
	}
	ext := name[len(name)-4:]
	if ext[0] != '.' || ext[1] != 'r' {
		return false
	}
	// Check if last two chars are digits
	return ext[2] >= '0' && ext[2] <= '9' && ext[3] >= '0' && ext[3] <= '9'
}

// extractRAR extracts RAR archives using unrar.
func extractRAR(dir string, archives []string) (*Result, error) {
	if _, err := exec.LookPath("unrar"); err != nil {
		return &Result{
			Message: "unrar not installed, skipping extraction",
		}, nil
	}

	// For multi-part RAR, we only need to extract the first part
	// unrar will automatically find the other parts
	firstPart := findFirstRARPart(archives)
	if firstPart == "" {
		firstPart = archives[0]
	}

	// Use basename since we run unrar in the archive directory
	firstPartBase := filepath.Base(firstPart)

	// Extract with overwrite, keeping directory structure, with timeout
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "unrar", "x", "-o+", "-y", firstPartBase)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return &Result{
			Extracted: false,
			Message:   "extraction timed out",
		}, nil
	}

	if err != nil {
		return &Result{
			Extracted: false,
			Message:   fmt.Sprintf("extraction failed: %s", string(output)),
		}, nil
	}

	// Parse extracted files from output
	files := parseUnrarOutput(string(output))

	return &Result{
		Extracted:    true,
		Files:        files,
		ArchiveFiles: archives,
		Message:      fmt.Sprintf("extracted %d files", len(files)),
	}, nil
}

// findFirstRARPart finds the first part of a multi-part RAR archive.
func findFirstRARPart(archives []string) string {
	for _, a := range archives {
		name := strings.ToLower(filepath.Base(a))
		// Modern RAR: .part1.rar, .part01.rar
		if strings.Contains(name, ".part1.rar") || strings.Contains(name, ".part01.rar") {
			return a
		}
		// Old RAR: just .rar (with .r00, .r01 for subsequent parts)
		if strings.HasSuffix(name, ".rar") && !strings.Contains(name, ".part") {
			return a
		}
	}
	return ""
}

// parseUnrarOutput parses the output of unrar to get extracted file names.
func parseUnrarOutput(output string) []string {
	var files []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Extracting") || strings.HasPrefix(line, "Creating") {
			// Format: "Extracting  filename   OK"
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				files = append(files, parts[1])
			}
		}
	}
	return files
}

// extractZIP extracts ZIP archives using unzip.
func extractZIP(dir string, archives []string) (*Result, error) {
	if _, err := exec.LookPath("unzip"); err != nil {
		return &Result{
			Message: "unzip not installed, skipping extraction",
		}, nil
	}

	var allFiles []string
	for _, archive := range archives {
		archiveBase := filepath.Base(archive)
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		cmd := exec.CommandContext(ctx, "unzip", "-o", archiveBase)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		cancel()

		if ctx.Err() == context.DeadlineExceeded {
			return &Result{
				Extracted: false,
				Message:   "extraction timed out",
			}, nil
		}

		if err != nil {
			return &Result{
				Extracted: false,
				Message:   fmt.Sprintf("extraction failed: %s", string(output)),
			}, nil
		}

		files := parseUnzipOutput(string(output))
		allFiles = append(allFiles, files...)
	}

	return &Result{
		Extracted:    true,
		Files:        allFiles,
		ArchiveFiles: archives,
		Message:      fmt.Sprintf("extracted %d files", len(allFiles)),
	}, nil
}

// parseUnzipOutput parses the output of unzip to get extracted file names.
func parseUnzipOutput(output string) []string {
	var files []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inflating:") || strings.HasPrefix(line, "extracting:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				files = append(files, strings.TrimSpace(parts[1]))
			}
		}
	}
	return files
}

// extract7Z extracts 7z archives using 7z.
func extract7Z(dir string, archives []string) (*Result, error) {
	if _, err := exec.LookPath("7z"); err != nil {
		return &Result{
			Message: "7z not installed, skipping extraction",
		}, nil
	}

	var allFiles []string
	for _, archive := range archives {
		archiveBase := filepath.Base(archive)
		ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
		cmd := exec.CommandContext(ctx, "7z", "x", "-y", archiveBase)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		cancel()

		if ctx.Err() == context.DeadlineExceeded {
			return &Result{
				Extracted: false,
				Message:   "extraction timed out",
			}, nil
		}

		if err != nil {
			return &Result{
				Extracted: false,
				Message:   fmt.Sprintf("extraction failed: %s", string(output)),
			}, nil
		}

		files := parse7zOutput(string(output))
		allFiles = append(allFiles, files...)
	}

	return &Result{
		Extracted:    true,
		Files:        allFiles,
		ArchiveFiles: archives,
		Message:      fmt.Sprintf("extracted %d files", len(allFiles)),
	}, nil
}

// parse7zOutput parses the output of 7z to get extracted file names.
func parse7zOutput(output string) []string {
	var files []string
	lines := strings.Split(output, "\n")
	inFileList := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "----------" {
			inFileList = true
			continue
		}
		if inFileList && line != "" && !strings.HasPrefix(line, "-----") {
			// 7z output format has various columns, file name is typically last
			files = append(files, line)
		}
	}
	return files
}

// Cleanup removes archive files after successful extraction.
func Cleanup(files []string) error {
	for _, f := range files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", f, err)
		}
	}
	return nil
}

// Flatten moves files from subdirectories to the target directory if no conflicts exist.
// It removes empty subdirectories after moving files.
// Returns the number of files moved.
func Flatten(dir string) (int, error) {
	moved := 0

	// Get list of files in subdirectories
	var filesToMove []struct {
		src  string
		name string
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			return nil
		}
		// Skip files already in the target directory
		if filepath.Dir(path) == dir {
			return nil
		}
		filesToMove = append(filesToMove, struct {
			src  string
			name string
		}{path, info.Name()})
		return nil
	})
	if err != nil {
		return 0, err
	}

	if len(filesToMove) == 0 {
		return 0, nil
	}

	// Check for conflicts (files with same name)
	nameCount := make(map[string]int)
	for _, f := range filesToMove {
		nameCount[f.name]++
	}

	// Also check against existing files in target dir
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			nameCount[e.Name()]++
		}
	}

	// Move files that don't have conflicts
	for _, f := range filesToMove {
		if nameCount[f.name] > 1 {
			continue // Skip files with name conflicts
		}
		dst := filepath.Join(dir, f.name)
		if err := os.Rename(f.src, dst); err == nil {
			moved++
		}
	}

	// Remove empty subdirectories (deepest first)
	var dirs []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && path != dir {
			dirs = append(dirs, path)
		}
		return nil
	})

	// Sort by depth (deepest first) to remove nested dirs first
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // Only succeeds if empty
	}

	return moved, nil
}

// MoveToParent moves all files from srcDir to its parent directory,
// then removes srcDir if empty. Returns number of files moved.
func MoveToParent(srcDir string) (int, error) {
	parentDir := filepath.Dir(srcDir)
	if parentDir == srcDir {
		return 0, nil // Already at root
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, err
	}

	moved := 0
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(parentDir, entry.Name())

		// Skip if destination already exists
		if _, err := os.Stat(dst); err == nil {
			continue
		}

		if err := os.Rename(src, dst); err == nil {
			moved++
		}
	}

	// Try to remove the now-empty directory
	os.Remove(srcDir)

	return moved, nil
}

// IsObfuscated checks if a filename (without extension) appears to be obfuscated.
// Returns true if it's 10+ alphanumeric characters with no dots, underscores, or hyphens.
func IsObfuscated(filename string) bool {
	// Get the base name without extension
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	return obfuscatedPattern.MatchString(base)
}

// DeobfuscateFiles renames obfuscated files in a directory using the provided base name.
// For example, "7lOrywGhSpJjcn6acjeZUtnYnDrXWSz4.mkv" becomes "My.Show.S01E01.mkv"
// Returns the number of files renamed.
func DeobfuscateFiles(dir string, baseName string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}

	renamed := 0
	usedNames := make(map[string]bool)

	// First pass: collect existing non-obfuscated names to avoid conflicts
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !IsObfuscated(entry.Name()) {
			usedNames[strings.ToLower(entry.Name())] = true
		}
	}

	// Second pass: rename obfuscated files
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !IsObfuscated(name) {
			continue
		}

		ext := filepath.Ext(name)
		newName := baseName + ext

		// Handle conflicts by adding a number
		if usedNames[strings.ToLower(newName)] {
			for i := 1; i < 100; i++ {
				newName = fmt.Sprintf("%s.%d%s", baseName, i, ext)
				if !usedNames[strings.ToLower(newName)] {
					break
				}
			}
		}

		src := filepath.Join(dir, name)
		dst := filepath.Join(dir, newName)

		if err := os.Rename(src, dst); err == nil {
			renamed++
			usedNames[strings.ToLower(newName)] = true
		}
	}

	return renamed, nil
}
