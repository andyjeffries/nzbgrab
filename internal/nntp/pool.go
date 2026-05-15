// Package nntp provides NNTP connection pooling for article retrieval.
package nntp

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Tensai75/nntpPool"

	"nzbgrab/internal/config"
)

// Pool wraps nntpPool.ConnectionPool with convenience methods.
type Pool struct {
	pool nntpPool.ConnectionPool
	cfg  *config.ServerConfig
}

// NewPool creates a new connection pool from config.
func NewPool(cfg *config.ServerConfig) (*Pool, error) {
	poolCfg := &nntpPool.Config{
		Name:                  cfg.Host,
		Host:                  cfg.Host,
		Port:                  uint32(cfg.Port),
		SSL:                   cfg.UseSSL(),
		SkipSSLCheck:          false,
		User:                  cfg.Username,
		Pass:                  cfg.Password,
		MaxConns:              uint32(cfg.Connections),
		ConnWaitTime:          5 * time.Second,
		IdleTimeout:           60 * time.Second,
		HealthCheck:           true,
		MaxTooManyConnsErrors: 3,
		MaxConnErrors:         5,
	}

	pool, err := nntpPool.New(poolCfg, 1) // Start with 1 initial connection
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	return &Pool{
		pool: pool,
		cfg:  cfg,
	}, nil
}

// Close closes all connections in the pool.
func (p *Pool) Close() {
	p.pool.Close()
}

// Connections returns the number of active and total connections.
func (p *Pool) Connections() (active, total uint32) {
	return p.pool.Conns()
}

// FetchArticle retrieves an article body by Message-ID.
// The Message-ID should be provided without angle brackets.
func (p *Pool) FetchArticle(ctx context.Context, messageID string) ([]byte, error) {
	conn, err := p.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting connection: %w", err)
	}
	defer p.pool.Put(conn)

	// Format Message-ID with angle brackets as required by NNTP
	fullID := "<" + messageID + ">"

	body, err := conn.Body(fullID)
	if err != nil {
		return nil, fmt.Errorf("fetching body for %s: %w", messageID, err)
	}

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading body for %s: %w", messageID, err)
	}

	return data, nil
}

// FetchArticleToWriter retrieves an article body and writes it to the provided writer.
// This is more efficient for large articles as it doesn't buffer the entire body.
func (p *Pool) FetchArticleToWriter(ctx context.Context, messageID string, w io.Writer) (int64, error) {
	conn, err := p.pool.Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting connection: %w", err)
	}
	defer p.pool.Put(conn)

	fullID := "<" + messageID + ">"

	body, err := conn.Body(fullID)
	if err != nil {
		return 0, fmt.Errorf("fetching body for %s: %w", messageID, err)
	}

	n, err := io.Copy(w, body)
	if err != nil {
		return n, fmt.Errorf("copying body for %s: %w", messageID, err)
	}

	return n, nil
}
