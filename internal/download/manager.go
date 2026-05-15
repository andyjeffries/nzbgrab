package download

import (
	"context"
	"sync"
	"time"

	"nzbgrab/internal/nntp"
	"nzbgrab/internal/nzb"
)

// State represents the download state of an NZB.
type State int

const (
	StateQueued State = iota
	StateDownloading
	StatePostProcessing
	StateCompleted
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateDownloading:
		return "downloading"
	case StatePostProcessing:
		return "processing"
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Job represents a single NZB download job.
type Job struct {
	ID       int
	NZB      *nzb.NZB
	State    State
	Progress Progress
	Error    error
	Started  time.Time
	Finished time.Time

	mu sync.RWMutex
}

// UpdateProgress updates the job's progress safely.
func (j *Job) UpdateProgress(p Progress) {
	j.mu.Lock()
	j.Progress = p
	j.mu.Unlock()
}

// GetProgress returns the current progress safely.
func (j *Job) GetProgress() Progress {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.Progress
}

// ManagerConfig contains configuration for the download manager.
type ManagerConfig struct {
	OutputDir       string
	MaxParallel     int   // Max simultaneous NZB downloads
	Connections     int   // NNTP connections per download
	BandwidthLimit  int64 // Bytes per second (0 = unlimited)
	SkipExtract     bool  // Skip archive extraction
}

// Manager orchestrates downloading multiple NZBs.
type Manager struct {
	cfg      ManagerConfig
	pool     *nntp.Pool
	jobs     []*Job
	jobChan  chan *Job
	doneChan chan struct{}

	mu sync.RWMutex
	wg sync.WaitGroup

	// Callbacks
	onProgress func(*Job)
	onComplete func(*Job)
}

// NewManager creates a new download manager.
func NewManager(pool *nntp.Pool, cfg ManagerConfig) *Manager {
	if cfg.MaxParallel < 1 {
		cfg.MaxParallel = 1
	}
	if cfg.MaxParallel > 10 {
		cfg.MaxParallel = 10
	}
	if cfg.Connections < 1 {
		cfg.Connections = 5
	}

	return &Manager{
		cfg:      cfg,
		pool:     pool,
		jobChan:  make(chan *Job, 100),
		doneChan: make(chan struct{}),
	}
}

// OnProgress sets the callback for progress updates.
func (m *Manager) OnProgress(fn func(*Job)) {
	m.onProgress = fn
}

// OnComplete sets the callback for job completion.
func (m *Manager) OnComplete(fn func(*Job)) {
	m.onComplete = fn
}

// Add adds NZB files to the download queue.
func (m *Manager) Add(nzbs ...*nzb.NZB) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, n := range nzbs {
		job := &Job{
			ID:    len(m.jobs) + 1,
			NZB:   n,
			State: StateQueued,
			Progress: Progress{
				TotalBytes:    n.TotalBytes(),
				TotalSegments: countSegments(n),
			},
		}
		m.jobs = append(m.jobs, job)
		m.jobChan <- job
	}
}

// Jobs returns all jobs.
func (m *Manager) Jobs() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs
}

// Start begins processing the download queue.
func (m *Manager) Start(ctx context.Context) {
	// Start worker goroutines
	for i := 0; i < m.cfg.MaxParallel; i++ {
		m.wg.Add(1)
		go m.worker(ctx)
	}
}

// Wait waits for all downloads to complete.
func (m *Manager) Wait() {
	close(m.jobChan)
	m.wg.Wait()
}

// Stop signals the manager to stop processing.
func (m *Manager) Stop() {
	close(m.doneChan)
}

func (m *Manager) worker(ctx context.Context) {
	defer m.wg.Done()

	for {
		select {
		case job, ok := <-m.jobChan:
			if !ok {
				return // Channel closed
			}
			m.processJob(ctx, job)

		case <-m.doneChan:
			return

		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) processJob(ctx context.Context, job *Job) {
	job.mu.Lock()
	job.State = StateDownloading
	job.Started = time.Now()
	job.mu.Unlock()

	// Create progress callback
	progressFn := func(p Progress) {
		job.UpdateProgress(p)
		if m.onProgress != nil {
			m.onProgress(job)
		}
	}

	// Download
	worker := NewParallelWorker(m.pool, m.cfg.OutputDir, job.NZB, m.cfg.Connections, progressFn)
	err := worker.Download(ctx)

	if err != nil {
		job.mu.Lock()
		job.State = StateFailed
		job.Error = err
		job.Finished = time.Now()
		job.mu.Unlock()
	} else {
		job.mu.Lock()
		job.State = StatePostProcessing
		job.mu.Unlock()

		// Post-processing (PAR2, extraction) will be added here
		// For now, just mark as complete
		job.mu.Lock()
		job.State = StateCompleted
		job.Finished = time.Now()
		job.mu.Unlock()
	}

	if m.onComplete != nil {
		m.onComplete(job)
	}
}

func countSegments(n *nzb.NZB) int {
	var total int
	for _, f := range n.Files {
		total += len(f.Segments)
	}
	return total
}

// Stats returns aggregate statistics for all jobs.
type Stats struct {
	TotalJobs     int
	Queued        int
	Downloading   int
	Completed     int
	Failed        int
	TotalBytes    int64
	DownloadBytes int64
}

// Stats returns current aggregate statistics.
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var s Stats
	s.TotalJobs = len(m.jobs)

	for _, job := range m.jobs {
		job.mu.RLock()
		switch job.State {
		case StateQueued:
			s.Queued++
		case StateDownloading, StatePostProcessing:
			s.Downloading++
		case StateCompleted:
			s.Completed++
		case StateFailed:
			s.Failed++
		}
		s.TotalBytes += job.Progress.TotalBytes
		s.DownloadBytes += job.Progress.DownloadedBytes
		job.mu.RUnlock()
	}

	return s
}
