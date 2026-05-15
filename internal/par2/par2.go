// Package par2 provides PAR2 verification and repair functionality.
package par2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PAR2 magic bytes
var par2Magic = []byte("PAR2\x00PKT")

// PAR2 packet type for file description (includes "PAR 2.0\x00" prefix)
var fileDescPacketType = []byte("PAR 2.0\x00FileDesc")

// Command timeout
const cmdTimeout = 60 * time.Second

// Result contains the result of PAR2 verification/repair.
type Result struct {
	Verified  bool     // True if verification passed
	Repaired  bool     // True if repair was needed and successful
	Message   string   // Status message
	Par2Files []string // PAR2 files found (for cleanup)
}

// Available checks if the par2 command is available.
func Available() bool {
	_, err := exec.LookPath("par2")
	return err == nil
}

// runWithTimeout runs a command with a timeout and kills it forcefully if needed.
func runWithTimeout(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	// Set process group so we can kill all children
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Capture output
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	// Wait for completion or timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return output.Bytes(), err
	case <-ctx.Done():
		// Kill the entire process group
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done // Wait for process to exit
		return output.Bytes(), ctx.Err()
	}
}

// Verify verifies files using PAR2 and repairs if necessary.
// dir is the directory containing the downloaded files.
// Returns the result of verification/repair.
func Verify(dir string) (*Result, error) {
	if !Available() {
		return &Result{
			Message: "par2 not installed, skipping verification",
		}, nil
	}

	// Find PAR2 files (by extension or magic bytes)
	par2Files, err := findPar2Files(dir)
	if err != nil {
		return nil, err
	}

	if len(par2Files) == 0 {
		return &Result{
			Verified: true,
			Message:  "no PAR2 files found",
		}, nil
	}

	// Find the index file (smallest PAR2 file)
	indexFile := findIndexFile(par2Files)
	if indexFile == "" {
		indexFile = par2Files[0]
	}

	// Try to deobfuscate files using PAR2 metadata
	// This may rename the indexFile itself
	Deobfuscate(dir, indexFile)

	// Re-find PAR2 files after deobfuscation (they may have been renamed)
	par2Files, err = findPar2Files(dir)
	if err != nil || len(par2Files) == 0 {
		return &Result{
			Verified: true,
			Message:  "no PAR2 files found after deobfuscation",
		}, nil
	}

	// Re-find index file (should now have .par2 extension)
	indexFile = findIndexFile(par2Files)
	if indexFile == "" {
		indexFile = par2Files[0]
	}

	// Verify the index file has .par2 extension (required by par2 command)
	if !strings.HasSuffix(strings.ToLower(indexFile), ".par2") {
		return &Result{
			Verified:  false,
			Par2Files: par2Files,
			Message:   "PAR2 index file missing .par2 extension",
		}, nil
	}

	// First try verification with timeout
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	// Use just the filename, not the full path (par2 runs in the directory)
	indexBasename := filepath.Base(indexFile)
	output, err := runWithTimeout(ctx, dir, "par2", "verify", indexBasename)

	if ctx.Err() == context.DeadlineExceeded {
		return &Result{
			Verified:  false,
			Par2Files: par2Files,
			Message:   "verification timed out",
		}, nil
	}

	if err == nil {
		return &Result{
			Verified:  true,
			Par2Files: par2Files,
			Message:   "verification passed",
		}, nil
	}

	// Check if repair is needed
	outputStr := string(output)
	if strings.Contains(outputStr, "Repair is required") ||
		strings.Contains(outputStr, "repair is required") {
		// Attempt repair with timeout
		repairCtx, repairCancel := context.WithTimeout(context.Background(), cmdTimeout)
		defer repairCancel()

		repairOutput, repairErr := runWithTimeout(repairCtx, dir, "par2", "repair", indexBasename)

		if repairCtx.Err() == context.DeadlineExceeded {
			return &Result{
				Verified:  false,
				Par2Files: par2Files,
				Message:   "repair timed out",
			}, nil
		}

		if repairErr == nil {
			return &Result{
				Verified:  true,
				Repaired:  true,
				Par2Files: par2Files,
				Message:   "repair successful",
			}, nil
		}

		return &Result{
			Verified:  false,
			Repaired:  false,
			Par2Files: par2Files,
			Message:   fmt.Sprintf("repair failed: %s", firstLine(string(repairOutput))),
		}, nil
	}

	// Some other error - return first line only
	return &Result{
		Verified:  false,
		Par2Files: par2Files,
		Message:   firstLine(outputStr),
	}, nil
}

// Deobfuscate renames obfuscated files to their original names using PAR2 metadata.
// It also renames obfuscated PAR2 files to have .par2 extension.
func Deobfuscate(dir string, par2File string) bool {
	renamed := false

	// First, rename ALL obfuscated PAR2 files to have .par2 extension
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		
		// Skip files that already have .par2 extension
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".par2") {
			continue
		}
		
		// Check if it's a PAR2 file by magic bytes
		if isPar2File(path) {
			newPath := path + ".par2"
			if err := os.Rename(path, newPath); err == nil {
				renamed = true
				// Update par2File reference if this was the index file
				if path == par2File {
					par2File = newPath
				}
			}
		}
	}

	// Read MD5 hashes from PAR2 for matching
	fileHashes, err := readPar2Hashes(par2File)
	if err != nil || len(fileHashes) == 0 {
		return renamed
	}

	// Re-read directory after PAR2 renames
	entries, err = os.ReadDir(dir)
	if err != nil {
		return renamed
	}

	// For each file in directory, check if it matches a PAR2 hash
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		currentPath := filepath.Join(dir, entry.Name())

		// Skip if already has a proper extension (not obfuscated)
		if hasKnownExtension(entry.Name()) {
			continue
		}

		// Skip PAR2 files (already handled)
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".par2") {
			continue
		}

		// Calculate MD5 of first 16KB to match against PAR2
		hash, err := calcFirst16KHash(currentPath)
		if err != nil {
			continue
		}

		// Look for matching hash in PAR2 data
		if origName, ok := fileHashes[hash]; ok {
			newPath := filepath.Join(dir, origName)
			// Don't overwrite existing files
			if _, err := os.Stat(newPath); err == nil {
				continue
			}
			if err := os.Rename(currentPath, newPath); err == nil {
				renamed = true
			}
		}
	}

	return renamed
}

// hasKnownExtension checks if a filename has a known media/archive extension.
func hasKnownExtension(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	known := []string{".mkv", ".avi", ".mp4", ".rar", ".zip", ".7z", ".par2", ".nfo", ".srr", ".srt", ".sub", ".idx"}
	for _, k := range known {
		if ext == k {
			return true
		}
	}
	return false
}

// calcFirst16KHash calculates MD5 of first 16KB of a file.
func calcFirst16KHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Read first 16KB
	buf := make([]byte, 16*1024)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	buf = buf[:n]

	// Calculate MD5
	h := md5.Sum(buf)
	return fmt.Sprintf("%x", h), nil
}

// readPar2Hashes reads MD5 hashes from a PAR2 file, returning map of hash16k -> filename.
func readPar2Hashes(par2File string) (map[string]string, error) {
	f, err := os.Open(par2File)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	hashes := make(map[string]string)

	// Read through PAR2 packets looking for FileDesc packets
	for {
		// Read packet header
		header := make([]byte, 64)
		_, err := io.ReadFull(f, header)
		if err != nil {
			break
		}

		// Check magic
		if !bytes.Equal(header[0:8], par2Magic) {
			break
		}

		// Get packet length
		packetLen := binary.LittleEndian.Uint64(header[8:16])
		if packetLen < 64 || packetLen > 100*1024*1024 {
			break
		}

		// Check if FileDesc packet (type is at bytes 48-64)
		if bytes.Equal(header[48:64], fileDescPacketType) {
			bodyLen := packetLen - 64
			body := make([]byte, bodyLen)
			_, err := io.ReadFull(f, body)
			if err != nil {
				break
			}

			// FileDesc body layout:
			// 0-16: file ID
			// 16-32: full file MD5 hash
			// 32-48: MD5 of first 16KB
			// 48-56: file length (8 bytes LE)
			// 56+: filename (ASCII, null terminated)
			if bodyLen > 56 {
				hash16k := fmt.Sprintf("%x", body[32:48])
				nameBytes := body[56:]
				// Find null terminator
				if idx := bytes.IndexByte(nameBytes, 0); idx > 0 {
					nameBytes = nameBytes[:idx]
				}
				name := string(nameBytes)
				if name != "" {
					hashes[hash16k] = name
				}
			}
		} else {
			// Skip packet
			f.Seek(int64(packetLen-64), io.SeekCurrent)
		}
	}

	return hashes, nil
}

// readPar2Filenames reads the original filenames from a PAR2 file.
func readPar2Filenames(par2File string) ([]string, error) {
	f, err := os.Open(par2File)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var filenames []string

	// Read through PAR2 packets looking for FileDesc packets
	for {
		header := make([]byte, 64)
		_, err := io.ReadFull(f, header)
		if err != nil {
			break
		}

		if !bytes.Equal(header[0:8], par2Magic) {
			break
		}

		packetLen := binary.LittleEndian.Uint64(header[8:16])
		if packetLen < 64 || packetLen > 100*1024*1024 {
			break
		}

		if bytes.Equal(header[48:64], fileDescPacketType) {
			bodyLen := packetLen - 64
			body := make([]byte, bodyLen)
			_, err := io.ReadFull(f, body)
			if err != nil {
				break
			}

			if bodyLen > 56 {
				nameBytes := body[56:]
				if idx := bytes.IndexByte(nameBytes, 0); idx > 0 {
					nameBytes = nameBytes[:idx]
				}
				name := string(nameBytes)
				if name != "" {
					filenames = append(filenames, name)
				}
			}
		} else {
			f.Seek(int64(packetLen-64), io.SeekCurrent)
		}
	}

	return filenames, nil
}

// findPar2Files finds all PAR2 files in a directory.
func findPar2Files(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		name := strings.ToLower(entry.Name())

		if strings.HasSuffix(name, ".par2") {
			files = append(files, fullPath)
			continue
		}

		if isPar2File(fullPath) {
			files = append(files, fullPath)
		}
	}

	return files, nil
}

// isPar2File checks if a file is a PAR2 file by reading its magic bytes.
func isPar2File(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	magic := make([]byte, len(par2Magic))
	n, err := io.ReadFull(f, magic)
	if err != nil || n != len(par2Magic) {
		return false
	}

	return bytes.Equal(magic, par2Magic)
}

// findIndexFile finds the PAR2 index file (smallest file).
func findIndexFile(files []string) string {
	var smallest string
	var smallestSize int64 = -1

	for _, f := range files {
		name := strings.ToLower(filepath.Base(f))

		if strings.HasSuffix(name, ".par2") && !strings.Contains(name, ".vol") {
			return f
		}

		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if smallestSize < 0 || info.Size() < smallestSize {
			smallest = f
			smallestSize = info.Size()
		}
	}

	return smallest
}

// Cleanup removes PAR2 files after successful verification.
func Cleanup(files []string) error {
	for _, f := range files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", f, err)
		}
	}
	return nil
}

// firstLine returns the first non-empty line of a string.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}
