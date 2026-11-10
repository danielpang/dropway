// SPDX-License-Identifier: FSL-1.1-Apache-2.0
package main

import (
	"context"
	"errors"
	"time"

	"github.com/danielpang/dropway/services/api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This release command only needs DATABASE_URL. It does not initialize telemetry,
// object storage, hosted execution, JWKS, HTTP listeners or background jobs.
func checkDeploymentSchema(databaseURL string) error {
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required for --check-schema")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL for schema check")
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return errors.New("cannot initialize database schema check")
	}
	defer pool.Close()
	return store.CheckSchema(ctx, pool)
}
