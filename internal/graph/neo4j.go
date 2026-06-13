package graph

import (
	"context"
	"fmt"
	"log"
	"note-memory/internal/config"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

// Driver wraps the Neo4j driver with session helpers.
// Nil Driver means Neo4j is not configured — all graph operations become no-ops.
type Driver struct {
	driver   neo4j.Driver
	database string
}

// NewDriver creates a Neo4j driver. Returns nil if URI is empty (optional component).
func NewDriver(cfg config.Neo4jConfig) (*Driver, error) {
	if cfg.URI == "" {
		log.Println("[neo4j] NEO4J_URI not set — knowledge graph disabled")
		return nil, nil
	}

	drv, err := neo4j.NewDriver(
		cfg.URI,
		neo4j.BasicAuth(cfg.User, cfg.Password, ""),
	)
	if err != nil {
		return nil, fmt.Errorf("create neo4j driver: %w", err)
	}

	if err := drv.VerifyConnectivity(context.Background()); err != nil {
		drv.Close(context.Background())
		return nil, fmt.Errorf("neo4j connectivity check failed: %w", err)
	}

	log.Printf("[neo4j] connected to %s", cfg.URI)
	return &Driver{driver: drv, database: "neo4j"}, nil
}

// Close shuts down the driver.
func (d *Driver) Close(ctx context.Context) {
	if d == nil || d.driver == nil {
		return
	}
	d.driver.Close(ctx)
}

// Session opens a new session for read/write operations.
func (d *Driver) Session(ctx context.Context) neo4j.Session {
	if d == nil || d.driver == nil {
		return nil
	}
	return d.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: d.database,
	})
}

// ExecuteWrite runs a write transaction.
func (d *Driver) ExecuteWrite(ctx context.Context, work func(tx neo4j.ManagedTransaction) (any, error)) (any, error) {
	if d == nil {
		return nil, nil
	}
	s := d.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)
	return s.ExecuteWrite(ctx, work)
}

// ExecuteRead runs a read transaction.
func (d *Driver) ExecuteRead(ctx context.Context, work func(tx neo4j.ManagedTransaction) (any, error)) (any, error) {
	if d == nil {
		return nil, nil
	}
	s := d.Session(ctx)
	if s == nil {
		return nil, nil
	}
	defer s.Close(ctx)
	return s.ExecuteRead(ctx, work)
}

// IsEnabled reports whether Neo4j is configured and available.
func (d *Driver) IsEnabled() bool {
	return d != nil && d.driver != nil
}
