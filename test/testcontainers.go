// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

//go:build integration

// Testcontainers helpers (TEST-006). These require Docker at runtime, so
// the file carries the `integration` build tag: `go test -tags=integration`.
// Unit tests in this package do not pull in the testcontainers dependency
// path. Use NewPostgresContainer / NewRedisContainer in integration tests
// where the in-memory SQLite or local-redis stand-ins are insufficient.

package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
)

// PostgresContainer wraps a testcontainers Postgres container for the
// duration of the test. The container is torn down via t.Cleanup.
type PostgresContainer struct {
	ConnString string
	Container  *postgres.PostgresContainer
}

// NewPostgresContainer starts a fresh Postgres container with the supplied
// image (defaults to postgres:16-alpine when empty). Returns the connection
// string usable by pgxpool or database/sql.
func NewPostgresContainer(t *testing.T, image string) *PostgresContainer {
	t.Helper()
	if image == "" {
		image = "postgres:16-alpine"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := postgres.Run(ctx, image,
		postgres.WithDatabase("ogontest"),
		postgres.WithUsername("ogon"),
		postgres.WithPassword("ogon"),
		postgres.WithInitScripts(),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("ogontest: start postgres: %v", err)
	}
	conn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("ogontest: postgres conn string: %v", err)
	}
	out := &PostgresContainer{ConnString: conn, Container: c}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Terminate(shutdownCtx); err != nil {
			t.Logf("ogontest: postgres terminate: %v", err)
		}
	})
	return out
}

// RedisContainer wraps a testcontainers Redis container.
type RedisContainer struct {
	Addr string
	C    *redis.RedisContainer
}

// NewRedisContainer starts a fresh Redis container with the supplied image
// (defaults to redis:7-alpine when empty). Returns the address (host:port)
// usable by go-redis or any other client.
func NewRedisContainer(t *testing.T, image string) *RedisContainer {
	t.Helper()
	if image == "" {
		image = "redis:7-alpine"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := redis.Run(ctx, image)
	if err != nil {
		t.Fatalf("ogontest: start redis: %v", err)
	}
	addr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ogontest: redis addr: %v", err)
	}
	out := &RedisContainer{Addr: addr, C: c}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Terminate(shutdownCtx); err != nil {
			t.Logf("ogontest: redis terminate: %v", err)
		}
	})
	return out
}

// GenericContainerRequest is a thin helper for arbitrary container images
// (e.g., mailpit, localstack). The caller is responsible for waiting on
// logs or ports.
func GenericContainerRequest(t *testing.T, req testcontainers.ContainerRequest) testcontainers.Container {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("ogontest: generic container: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.Terminate(shutdownCtx); err != nil {
			t.Logf("ogontest: container terminate: %v", err)
		}
	})
	return c
}

// DockerAvailable reports whether the Docker socket is reachable. Tests
// that require testcontainers should skip when false.
func DockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer provider.Close()
	return provider.Health(ctx) == nil
}

// SkipIfNoDocker skips the test if Docker is unavailable.
func SkipIfNoDocker(t *testing.T, reason string) {
	t.Helper()
	if !DockerAvailable() {
		t.Skipf("ogontest: docker unavailable: %s", reason)
	}
}

var _ = fmt.Sprintf // keep fmt imported even when helpers above grow
