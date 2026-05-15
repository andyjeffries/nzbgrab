// Package tui provides terminal UI for download progress.
package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

	"nzbgrab/internal/download"
)

// UI manages the terminal user interface.
type UI struct {
	progress *mpb.Progress
	bars     map[int]*barState
	mu       sync.Mutex
	quiet    bool
	out      io.Writer
}

type barState struct {
	bar       *mpb.Bar
	job       *download.Job
	lastBytes int64
	lastTime  time.Time
}

// New creates a new UI.
func New(out io.Writer, quiet bool) *UI {
	if quiet {
		return &UI{quiet: true, out: out}
	}

	p := mpb.New(
		mpb.WithOutput(out),
		mpb.WithRefreshRate(100*time.Millisecond),
	)

	return &UI{
		progress: p,
		bars:     make(map[int]*barState),
		out:      out,
	}
}

// AddJob adds a job to the UI.
func (u *UI) AddJob(job *download.Job) {
	if u.quiet {
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	// Truncate name if too long
	name := job.NZB.Name
	if len(name) > 40 {
		name = name[:37] + "..."
	}

	bar := u.progress.AddBar(job.Progress.TotalBytes,
		mpb.PrependDecorators(
			decor.Name(name, decor.WCSyncSpaceR),
			decor.CountersKibiByte("% .1f / % .1f", decor.WCSyncSpace),
		),
		mpb.AppendDecorators(
			decor.NewPercentage("%.1f", decor.WCSyncSpace),
			decor.EwmaSpeed(decor.SizeB1024(0), "% .1f", 30, decor.WCSyncSpace),
			decor.OnComplete(
				decor.EwmaETA(decor.ET_STYLE_GO, 30, decor.WCSyncSpace),
				"done",
			),
		),
	)

	u.bars[job.ID] = &barState{
		bar:      bar,
		job:      job,
		lastTime: time.Now(),
	}
}

// UpdateJob updates the progress bar for a job.
func (u *UI) UpdateJob(job *download.Job) {
	if u.quiet {
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	state, ok := u.bars[job.ID]
	if !ok {
		return
	}

	progress := job.GetProgress()
	delta := progress.DownloadedBytes - state.lastBytes

	if delta > 0 {
		state.bar.EwmaIncrInt64(delta, time.Since(state.lastTime))
		state.lastBytes = progress.DownloadedBytes
		state.lastTime = time.Now()
	}
}

// CompleteJob marks a job as complete.
func (u *UI) CompleteJob(job *download.Job) {
	if u.quiet {
		fmt.Fprintf(u.out, "%s: %s\n", job.State.String(), job.NZB.Name)
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	state, ok := u.bars[job.ID]
	if !ok {
		return
	}

	// Ensure bar is at 100%
	state.bar.SetTotal(job.Progress.TotalBytes, true)
}

// Wait waits for all progress bars to finish rendering.
func (u *UI) Wait() {
	if u.quiet {
		return
	}
	u.progress.Wait()
}

// QueuedJobs displays queued jobs below the progress bars.
type QueuedDisplay struct {
	mu     sync.Mutex
	jobs   []*download.Job
	out    io.Writer
	ticker *time.Ticker
	done   chan struct{}
}

// NewQueuedDisplay creates a display for queued jobs.
func NewQueuedDisplay(out io.Writer) *QueuedDisplay {
	return &QueuedDisplay{
		out:  out,
		done: make(chan struct{}),
	}
}

// SetJobs updates the list of queued jobs.
func (q *QueuedDisplay) SetJobs(jobs []*download.Job) {
	q.mu.Lock()
	q.jobs = jobs
	q.mu.Unlock()
}

// FormatProgress returns a formatted progress string for a job.
func FormatProgress(job *download.Job) string {
	p := job.GetProgress()
	pct := float64(0)
	if p.TotalBytes > 0 {
		pct = float64(p.DownloadedBytes) / float64(p.TotalBytes) * 100
	}

	return fmt.Sprintf("[%5.1f%%] %s / %s - %s",
		pct,
		humanize.Bytes(uint64(p.DownloadedBytes)),
		humanize.Bytes(uint64(p.TotalBytes)),
		job.NZB.Name)
}

// PrintSummary prints a summary of completed downloads.
func PrintSummary(out io.Writer, jobs []*download.Job) {
	var completed, failed int
	var totalBytes int64
	var totalTime time.Duration

	for _, job := range jobs {
		if job.State == download.StateCompleted {
			completed++
			totalBytes += job.Progress.TotalBytes
			if !job.Finished.IsZero() && !job.Started.IsZero() {
				totalTime += job.Finished.Sub(job.Started)
			}
		} else if job.State == download.StateFailed {
			failed++
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Completed: %d", completed)
	if failed > 0 {
		fmt.Fprintf(out, ", Failed: %d", failed)
	}
	fmt.Fprintln(out)

	if totalBytes > 0 && totalTime > 0 {
		speed := float64(totalBytes) / totalTime.Seconds()
		fmt.Fprintf(out, "Total: %s at %s/s\n",
			humanize.Bytes(uint64(totalBytes)),
			humanize.Bytes(uint64(speed)))
	}

	// Print any errors
	for _, job := range jobs {
		if job.Error != nil {
			fmt.Fprintf(out, "Error (%s): %v\n", job.NZB.Name, job.Error)
		}
	}
}

// SimpleProgress provides a simple text-based progress display.
type SimpleProgress struct {
	out       io.Writer
	startTime time.Time
	lastPrint time.Time
}

// NewSimpleProgress creates a simple progress display.
func NewSimpleProgress(out io.Writer) *SimpleProgress {
	return &SimpleProgress{
		out:       out,
		startTime: time.Now(),
	}
}

// Update prints progress for a job.
func (s *SimpleProgress) Update(job *download.Job) {
	// Rate limit output to avoid flooding
	if time.Since(s.lastPrint) < 100*time.Millisecond {
		return
	}
	s.lastPrint = time.Now()

	p := job.GetProgress()
	pct := float64(0)
	if p.TotalBytes > 0 {
		pct = float64(p.DownloadedBytes) / float64(p.TotalBytes) * 100
	}

	// Calculate speed
	elapsed := time.Since(s.startTime).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(p.DownloadedBytes) / elapsed
	}

	// Build progress bar
	barWidth := 30
	filled := int(pct / 100 * float64(barWidth))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	fmt.Fprintf(s.out, "\r[%s] %5.1f%% %s / %s @ %s/s - %s    ",
		bar,
		pct,
		humanize.Bytes(uint64(p.DownloadedBytes)),
		humanize.Bytes(uint64(p.TotalBytes)),
		humanize.Bytes(uint64(speed)),
		truncateName(job.NZB.Name, 30))
}

// Complete prints completion message.
func (s *SimpleProgress) Complete(job *download.Job) {
	elapsed := time.Since(s.startTime)
	p := job.GetProgress()

	fmt.Fprintf(s.out, "\r%s\r", strings.Repeat(" ", 100)) // Clear line
	if job.State == download.StateCompleted {
		speed := float64(p.TotalBytes) / elapsed.Seconds()
		fmt.Fprintf(s.out, "✓ %s (%s at %s/s)\n",
			job.NZB.Name,
			humanize.Bytes(uint64(p.TotalBytes)),
			humanize.Bytes(uint64(speed)))
	} else {
		fmt.Fprintf(s.out, "✗ %s: %v\n", job.NZB.Name, job.Error)
	}
}

func truncateName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	return name[:maxLen-3] + "..."
}
