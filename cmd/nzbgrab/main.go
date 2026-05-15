package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"nzbgrab/internal/config"
	"nzbgrab/internal/download"
	"nzbgrab/internal/extract"
	"nzbgrab/internal/nntp"
	"nzbgrab/internal/nzb"
	"nzbgrab/internal/par2"
)

var (
	version = "0.1.0"

	// Flags
	outputDir  string
	limitStr   string
	parallel   int
	noExtract  bool
	quiet      bool
	configPath string
)

// nzbResult holds the result of processing a single NZB.
type nzbResult struct {
	Name          string
	Success       bool
	DownloadErr   error
	Par2Result    *par2.Result
	ExtractResult *extract.Result
	CleanupErr    error
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "nzbgrab [flags] <file.nzb> [file2.nzb ...]",
		Short: "Download NZB files from Usenet",
		Long: `nzbgrab is a command-line NZB downloader with parallel downloads,
PAR2 verification/repair, and automatic archive extraction.

Configuration is read from ~/.config/nzbgrab/config.toml`,
		Args:    cobra.MinimumNArgs(1),
		Version: version,
		RunE:    run,
	}

	rootCmd.Flags().StringVarP(&outputDir, "output", "o", "", "Download directory (overrides config)")
	rootCmd.Flags().StringVarP(&limitStr, "limit", "l", "", "Bandwidth limit (e.g., 10M, 500K, 1G)")
	rootCmd.Flags().IntVarP(&parallel, "parallel", "p", 0, "Max simultaneous NZB downloads (overrides config)")
	rootCmd.Flags().BoolVarP(&noExtract, "no-extract", "n", false, "Skip archive extraction")
	rootCmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress progress bars")
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Load config
	cfgPath := configPath
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Apply flag overrides
	if outputDir != "" {
		cfg.Download.Dir = expandPath(outputDir)
	}
	if parallel > 0 {
		cfg.Download.Parallel = parallel
	}

	// Parse bandwidth limit
	var bandwidthLimit int64
	if limitStr != "" {
		bandwidthLimit = parseBandwidth(limitStr)
		if bandwidthLimit == 0 {
			return fmt.Errorf("invalid bandwidth limit: %s", limitStr)
		}
	}

	// Check for required external tools
	if err := checkExternalTools(); err != nil {
		return err
	}

	// Parse NZB files
	var nzbs []*nzb.NZB
	for _, path := range args {
		n, err := nzb.Parse(path)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		nzbs = append(nzbs, n)
	}

	// Print summary
	var totalBytes int64
	for _, n := range nzbs {
		totalBytes += n.TotalBytes()
	}

	if !quiet {
		fmt.Printf("nzbgrab v%s\n\n", version)
		fmt.Printf("Server: %s:%d\n", cfg.Server.Host, cfg.Server.Port)
		fmt.Printf("Downloads: %d NZB(s), %s total\n", len(nzbs), humanize.Bytes(uint64(totalBytes)))
		fmt.Printf("Output: %s\n", cfg.Download.Dir)
		fmt.Printf("Parallel NZBs: %d\n", cfg.Download.Parallel)
		if bandwidthLimit > 0 {
			fmt.Printf("Limit: %s/s\n", humanize.Bytes(uint64(bandwidthLimit)))
		}
		fmt.Println()
	}

	// Create connection pool
	pool, err := nntp.NewPool(&cfg.Server)
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer pool.Close()

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nInterrupted, shutting down...")
		cancel()
	}()

	// Create progress display
	var progress *mpb.Progress
	if !quiet {
		progress = mpb.New(
			mpb.WithOutput(os.Stdout),
			mpb.WithRefreshRate(100*time.Millisecond),
		)
	}

	// Track results
	var completedCount, failedCount atomic.Int32
	var downloadedBytes atomic.Int64
	startTime := time.Now()

	// Collect results for printing after progress bars complete
	var resultsMu sync.Mutex
	var allResults []nzbResult

	// Create all progress bars upfront (so queued NZBs are visible)
	bars := make(map[string]*mpb.Bar)
	if progress != nil {
		for _, n := range nzbs {
			name := n.Name
			if len(name) > 50 {
				name = name[:47] + "..."
			}
			bars[n.Name] = progress.AddBar(n.TotalBytes(),
				mpb.PrependDecorators(
					decor.Name(name, decor.WCSyncSpaceR),
				),
				mpb.AppendDecorators(
					decor.CountersKibiByte("% .1f/% .1f", decor.WCSyncSpace),
					decor.NewPercentage(" %.1f", decor.WCSyncSpace),
					decor.EwmaSpeed(decor.SizeB1024(0), " % .1f", 30, decor.WCSyncSpace),
					decor.OnComplete(
						decor.EwmaETA(decor.ET_STYLE_GO, 30, decor.WCSyncSpace),
						"done",
					),
				),
			)
		}
	}

	// Create semaphore for parallel NZB downloads
	sem := make(chan struct{}, cfg.Download.Parallel)
	var wg sync.WaitGroup

	// Process all NZBs
	for _, n := range nzbs {
		if ctx.Err() != nil {
			break
		}

		n := n // capture for goroutine
		bar := bars[n.Name]
		wg.Add(1)

		go func() {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			// Process this NZB
			result := processNZB(ctx, n, pool, cfg, bar, bandwidthLimit)

			resultsMu.Lock()
			allResults = append(allResults, result)
			resultsMu.Unlock()

			if result.Success {
				completedCount.Add(1)
				downloadedBytes.Add(n.TotalBytes())
			} else {
				failedCount.Add(1)
			}
		}()
	}

	// Wait for all downloads to complete
	wg.Wait()

	// Wait for progress bars to finish
	if progress != nil {
		progress.Wait()
	}

	// Print summary
	elapsed := time.Since(startTime)
	completed := int(completedCount.Load())
	failed := int(failedCount.Load())
	downloaded := downloadedBytes.Load()

	if !quiet {
		fmt.Println()

		// Only print individual results if there were errors
		for _, result := range allResults {
			printResult(result)
		}

		// Single summary line
		if downloaded > 0 && elapsed > 0 {
			speed := float64(downloaded) / elapsed.Seconds()
			fmt.Printf("Completed %d/%d, %s in %s (%s/s)\n",
				completed, len(nzbs),
				humanize.Bytes(uint64(downloaded)),
				elapsed.Round(time.Second),
				humanize.Bytes(uint64(speed)))
		} else {
			fmt.Printf("Completed %d/%d\n", completed, len(nzbs))
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d download(s) failed", failed)
	}

	return nil
}

// printResult prints the result of processing an NZB (only errors).
func printResult(r nzbResult) {
	if r.DownloadErr != nil {
		fmt.Printf("%s: download failed: %v\n", r.Name, r.DownloadErr)
		return
	}

	// Only print PAR2 failures
	if r.Par2Result != nil && !r.Par2Result.Verified {
		fmt.Printf("%s: %s\n", r.Name, r.Par2Result.Message)
	}

	// Only print extraction failures
	if r.ExtractResult != nil && !r.ExtractResult.Extracted && r.ExtractResult.Message != "" && r.ExtractResult.Message != "no archives found" {
		fmt.Printf("%s: %s\n", r.Name, r.ExtractResult.Message)
	}
}

// processNZB downloads and post-processes a single NZB.
func processNZB(ctx context.Context, n *nzb.NZB, pool *nntp.Pool, cfg *config.Config,
	bar *mpb.Bar, bandwidthLimit int64) nzbResult {

	result := nzbResult{Name: n.Name, Success: true}

	// Create worker
	var lastBytes int64
	var lastTime = time.Now()
	var mu sync.Mutex

	worker := download.NewParallelWorkerWithLimit(pool, cfg.Download.Dir, n, cfg.Server.Connections, bandwidthLimit, func(p download.Progress) {
		if bar != nil {
			mu.Lock()
			delta := p.DownloadedBytes - lastBytes
			if delta > 0 {
				bar.EwmaIncrInt64(delta, time.Since(lastTime))
				lastBytes = p.DownloadedBytes
				lastTime = time.Now()
			}
			mu.Unlock()
		}
	})

	// Download
	err := worker.Download(ctx)

	if err != nil {
		if bar != nil {
			bar.Abort(true)
		}
		result.Success = false
		result.DownloadErr = err
		return result
	}

	// Mark bar as complete
	if bar != nil {
		bar.SetCurrent(n.TotalBytes())
		bar.SetTotal(n.TotalBytes(), true)
	}

	// Post-processing
	downloadDir := filepath.Join(cfg.Download.Dir, sanitizeFilename(n.Name))

	// PAR2 verification
	par2Result, _ := par2.Verify(downloadDir)
	result.Par2Result = par2Result

	// Extract archives (recursively)
	if !noExtract {
		extractResult := extractRecursive(downloadDir)
		result.ExtractResult = extractResult

		// Cleanup archive files after successful extraction
		if extractResult != nil && extractResult.Extracted && len(extractResult.ArchiveFiles) > 0 {
			result.CleanupErr = extract.Cleanup(extractResult.ArchiveFiles)
		}

		// Flatten: move files from subdirectories to download folder
		extract.Flatten(downloadDir)
	}

	// Cleanup PAR2 files after successful verification
	if par2Result != nil && par2Result.Verified && len(par2Result.Par2Files) > 0 {
		par2.Cleanup(par2Result.Par2Files)
	}

	// Move files to output directory and remove download subfolder
	extract.MoveToParent(downloadDir)

	// Rename obfuscated files using NZB name
	extract.DeobfuscateFiles(cfg.Download.Dir, n.Name)

	// Remove the NZB file on success
	if n.Path != "" {
		os.Remove(n.Path)
	}

	return result
}

// extractRecursive extracts archives, including nested archives.
func extractRecursive(dir string) *extract.Result {
	var allFiles []string
	var allArchiveFiles []string
	extracted := false
	maxDepth := 5 // Prevent infinite loops

	var extractDir func(d string, depth int)
	extractDir = func(d string, depth int) {
		if depth > maxDepth {
			return
		}

		result, err := extract.Extract(d)
		if err != nil || result == nil || !result.Extracted || len(result.Files) == 0 {
			return
		}

		extracted = true
		allFiles = append(allFiles, result.Files...)
		allArchiveFiles = append(allArchiveFiles, result.ArchiveFiles...)

		// Check all subdirectories for more archives
		entries, _ := os.ReadDir(d)
		for _, entry := range entries {
			if entry.IsDir() {
				extractDir(filepath.Join(d, entry.Name()), depth+1)
			}
		}

		// Try extracting again in case new archives appeared (from nested extraction)
		result2, err := extract.Extract(d)
		if err == nil && result2 != nil && result2.Extracted && len(result2.Files) > 0 {
			allFiles = append(allFiles, result2.Files...)
			allArchiveFiles = append(allArchiveFiles, result2.ArchiveFiles...)
		}
	}

	extractDir(dir, 0)

	if !extracted {
		return &extract.Result{
			Extracted: true,
			Message:   "no archives found",
		}
	}

	return &extract.Result{
		Extracted:    true,
		Files:        allFiles,
		ArchiveFiles: allArchiveFiles,
		Message:      fmt.Sprintf("extracted %d files", len(allFiles)),
	}
}

// checkExternalTools checks if required external tools are available.
func checkExternalTools() error {
	var missing []string

	if !par2.Available() {
		missing = append(missing, "par2 (for PAR2 verification/repair)")
	}

	tools := extract.Available()
	if !tools["unrar"] && !tools["7z"] {
		missing = append(missing, "unrar or 7z (for RAR extraction)")
	}
	if !tools["unzip"] && !tools["7z"] {
		missing = append(missing, "unzip or 7z (for ZIP extraction)")
	}

	if len(missing) > 0 {
		fmt.Println("Warning: The following tools are not installed:")
		for _, tool := range missing {
			fmt.Printf("  - %s\n", tool)
		}
		fmt.Println()
		fmt.Print("Do you want to just download the files without verification/extraction? (Y/n) ")

		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading response: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "" && response != "y" && response != "yes" {
			return fmt.Errorf("missing required tools")
		}

		fmt.Println()
	}

	return nil
}

// parseBandwidth parses a human-readable bandwidth limit.
func parseBandwidth(s string) int64 {
	return download.ParseBandwidthLimit(s)
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// sanitizeFilename removes or replaces problematic characters.
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "'",
		"<", "-",
		">", "-",
		"|", "-",
	)
	return replacer.Replace(name)
}
