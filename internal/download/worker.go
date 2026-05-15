// Package download handles downloading NZB files.
package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"nzbgrab/internal/nntp"
	"nzbgrab/internal/nzb"
	"nzbgrab/internal/yenc"
)

// Progress tracks download progress.
type Progress struct {
	TotalBytes      int64 // Total bytes to download
	DownloadedBytes int64 // Bytes downloaded so far
	TotalSegments   int   // Total segments to download
	DoneSegments    int   // Segments completed
	CurrentFile     string // Current file being downloaded
}

// ProgressFunc is called periodically with progress updates.
type ProgressFunc func(Progress)

// Worker downloads a single NZB file.
type Worker struct {
	pool       *nntp.Pool
	outputDir  string
	nzbFile    *nzb.NZB
	progressFn ProgressFunc

	// Progress tracking
	progress Progress
	mu       sync.Mutex
}

// NewWorker creates a new download worker.
func NewWorker(pool *nntp.Pool, outputDir string, nzbFile *nzb.NZB, progressFn ProgressFunc) *Worker {
	// Calculate totals
	var totalBytes int64
	var totalSegments int
	for _, f := range nzbFile.Files {
		totalBytes += f.Bytes
		totalSegments += len(f.Segments)
	}

	return &Worker{
		pool:       pool,
		outputDir:  outputDir,
		nzbFile:    nzbFile,
		progressFn: progressFn,
		progress: Progress{
			TotalBytes:    totalBytes,
			TotalSegments: totalSegments,
		},
	}
}

// Download downloads all files in the NZB.
func (w *Worker) Download(ctx context.Context) error {
	// Create output directory named after NZB
	dir := filepath.Join(w.outputDir, sanitizeFilename(w.nzbFile.Name))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Download each file
	for _, file := range w.nzbFile.Files {
		if err := ctx.Err(); err != nil {
			return err
		}

		w.mu.Lock()
		w.progress.CurrentFile = file.Filename
		w.mu.Unlock()
		w.reportProgress()

		if err := w.downloadFile(ctx, dir, file); err != nil {
			return fmt.Errorf("downloading %s: %w", file.Filename, err)
		}
	}

	return nil
}

// downloadFile downloads a single file (all its segments).
func (w *Worker) downloadFile(ctx context.Context, dir string, file *nzb.File) error {
	if file.Filename == "" {
		return fmt.Errorf("file has no filename")
	}

	outPath := filepath.Join(dir, file.Filename)
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	// Download segments in order
	for _, segment := range file.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}

		data, err := w.downloadSegment(ctx, segment)
		if err != nil {
			return fmt.Errorf("segment %d: %w", segment.Number, err)
		}

		if _, err := outFile.Write(data); err != nil {
			return fmt.Errorf("writing segment %d: %w", segment.Number, err)
		}

		// Update progress
		w.mu.Lock()
		w.progress.DownloadedBytes += int64(len(data))
		w.progress.DoneSegments++
		w.mu.Unlock()
		w.reportProgress()
	}

	return nil
}

// downloadSegment downloads and decodes a single segment.
func (w *Worker) downloadSegment(ctx context.Context, segment *nzb.Segment) ([]byte, error) {
	data, err := w.pool.FetchArticle(ctx, segment.MessageID)
	if err != nil {
		return nil, err
	}

	result, err := yenc.DecodeBytes(data)
	if err != nil {
		return nil, fmt.Errorf("yenc decode: %w", err)
	}

	return result.Data, nil
}

func (w *Worker) reportProgress() {
	if w.progressFn != nil {
		w.mu.Lock()
		p := w.progress
		w.mu.Unlock()
		w.progressFn(p)
	}
}

// sanitizeFilename removes or replaces characters that are problematic in filenames.
func sanitizeFilename(name string) string {
	// Replace problematic characters
	replacer := map[rune]rune{
		'/':  '-',
		'\\': '-',
		':':  '-',
		'*':  '-',
		'?':  '-',
		'"':  '\'',
		'<':  '-',
		'>':  '-',
		'|':  '-',
	}

	result := make([]rune, 0, len(name))
	for _, r := range name {
		if rep, ok := replacer[r]; ok {
			result = append(result, rep)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// ParallelWorker downloads segments in parallel using multiple connections.
type ParallelWorker struct {
	pool        *nntp.Pool
	outputDir   string
	nzbFile     *nzb.NZB
	progressFn  ProgressFunc
	concurrency int
	rateLimiter *RateLimiter

	// Progress tracking (atomic for concurrent access)
	downloadedBytes atomic.Int64
	doneSegments    atomic.Int64
	totalBytes      int64
	totalSegments   int
	currentFile     atomic.Value // string
}

// NewParallelWorker creates a new parallel download worker.
func NewParallelWorker(pool *nntp.Pool, outputDir string, nzbFile *nzb.NZB, concurrency int, progressFn ProgressFunc) *ParallelWorker {
	return NewParallelWorkerWithLimit(pool, outputDir, nzbFile, concurrency, 0, progressFn)
}

// NewParallelWorkerWithLimit creates a new parallel download worker with bandwidth limiting.
func NewParallelWorkerWithLimit(pool *nntp.Pool, outputDir string, nzbFile *nzb.NZB, concurrency int, bytesPerSec int64, progressFn ProgressFunc) *ParallelWorker {
	var totalBytes int64
	var totalSegments int
	for _, f := range nzbFile.Files {
		totalBytes += f.Bytes
		totalSegments += len(f.Segments)
	}

	w := &ParallelWorker{
		pool:          pool,
		outputDir:     outputDir,
		nzbFile:       nzbFile,
		progressFn:    progressFn,
		concurrency:   concurrency,
		rateLimiter:   NewRateLimiter(bytesPerSec),
		totalBytes:    totalBytes,
		totalSegments: totalSegments,
	}
	w.currentFile.Store("")
	return w
}

// segmentJob represents a segment to download.
type segmentJob struct {
	file    *nzb.File
	segment *nzb.Segment
	index   int // index within file's segments
}

// segmentResult contains the downloaded segment data.
type segmentResult struct {
	job      segmentJob
	data     []byte
	filename string // from yEnc header (only set for first segment)
	err      error
}

// Download downloads all files in the NZB using parallel connections.
func (w *ParallelWorker) Download(ctx context.Context) error {
	// Create output directory
	dir := filepath.Join(w.outputDir, sanitizeFilename(w.nzbFile.Name))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Process each file sequentially, but download segments in parallel
	for _, file := range w.nzbFile.Files {
		if err := ctx.Err(); err != nil {
			return err
		}

		w.currentFile.Store(file.Filename)
		w.reportProgress()

		if err := w.downloadFileParallel(ctx, dir, file); err != nil {
			return fmt.Errorf("downloading %s: %w", file.Filename, err)
		}
	}

	return nil
}

// downloadFileParallel downloads a file's segments in parallel.
func (w *ParallelWorker) downloadFileParallel(ctx context.Context, dir string, file *nzb.File) error {
	if file.Filename == "" {
		return fmt.Errorf("file has no filename")
	}

	// Create channels
	jobs := make(chan segmentJob, len(file.Segments))
	results := make(chan segmentResult, len(file.Segments))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				data, filename, err := w.downloadSegment(ctx, job.segment)
				results <- segmentResult{job: job, data: data, filename: filename, err: err}
			}
		}()
	}

	// Send jobs
	go func() {
		for i, seg := range file.Segments {
			jobs <- segmentJob{file: file, segment: seg, index: i}
		}
		close(jobs)
	}()

	// Collect results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Buffer for out-of-order segments
	segments := make([][]byte, len(file.Segments))
	var firstErr error
	var realFilename string // filename from yEnc header

	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("segment %d: %w", result.job.segment.Number, result.err)
			}
			continue
		}
		segments[result.job.index] = result.data
		
		// Capture filename from first segment's yEnc header
		if result.filename != "" && realFilename == "" {
			realFilename = result.filename
		}

		// Update progress
		w.downloadedBytes.Add(int64(len(result.data)))
		w.doneSegments.Add(1)
		w.reportProgress()
	}

	if firstErr != nil {
		return firstErr
	}

	// Use the real filename from yEnc if available, otherwise fall back to NZB subject
	outputFilename := file.Filename
	if realFilename != "" {
		outputFilename = realFilename
	}

	// Write file in order
	outPath := filepath.Join(dir, outputFilename)
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	for i, data := range segments {
		if data == nil {
			return fmt.Errorf("missing segment %d", i+1)
		}
		if _, err := outFile.Write(data); err != nil {
			return fmt.Errorf("writing segment %d: %w", i+1, err)
		}
	}

	return nil
}

// downloadSegment downloads and decodes a single segment.
// Returns the decoded data and the filename from yEnc header (if present).
func (w *ParallelWorker) downloadSegment(ctx context.Context, segment *nzb.Segment) ([]byte, string, error) {
	data, err := w.pool.FetchArticle(ctx, segment.MessageID)
	if err != nil {
		return nil, "", err
	}

	result, err := yenc.DecodeBytes(data)
	if err != nil {
		return nil, "", fmt.Errorf("yenc decode: %w", err)
	}

	// Apply rate limiting after receiving data
	if w.rateLimiter != nil {
		if err := w.rateLimiter.Wait(ctx, len(result.Data)); err != nil {
			return nil, "", err
		}
	}

	return result.Data, result.Header.Name, nil
}

func (w *ParallelWorker) reportProgress() {
	if w.progressFn != nil {
		currentFile, _ := w.currentFile.Load().(string)
		w.progressFn(Progress{
			TotalBytes:      w.totalBytes,
			DownloadedBytes: w.downloadedBytes.Load(),
			TotalSegments:   w.totalSegments,
			DoneSegments:    int(w.doneSegments.Load()),
			CurrentFile:     currentFile,
		})
	}
}

// Progress returns the current download progress.
func (w *ParallelWorker) Progress() Progress {
	currentFile, _ := w.currentFile.Load().(string)
	return Progress{
		TotalBytes:      w.totalBytes,
		DownloadedBytes: w.downloadedBytes.Load(),
		TotalSegments:   w.totalSegments,
		DoneSegments:    int(w.doneSegments.Load()),
		CurrentFile:     currentFile,
	}
}
