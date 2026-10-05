// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// BaseModel — the conventional embedded struct every OgonRecord model
// inherits. Fields are deliberately value types so embedding is by-value
// and structs remain comparable. Soft-delete is encoded via DeletedAt
// (NULL = live row); the query builder omits soft-deleted rows by default.

package record

import (
	"time"

	"github.com/OgonFrameworks/ogon.go/record/types"
)

// BaseModel carries the conventional primary key and lifecycle timestamps
// shared by every OgonRecord model. Embedding is by-value:
//
//	type User struct {
//	    record.BaseModel
//	    Email string `ogon:"unique,email"`
//	}
//
// All four fields map to well-known columns so the scanner and migration
// engine can drive off reflection without per-model codegen (DATA-027 — the
// generated scanners arrive in a future phase).
//
// The fields omit the explicit `type:` tag deliberately so the migration
// engine can pick the dialect-appropriate column type (TEXT for SQLite,
// uuid/timestamptz for Postgres).
type BaseModel struct {
	// ID is the primary key. The zero value is treated as "unset" — saving
	// a zero-ID model performs an INSERT and back-fills the ID.
	ID types.UUID `ogon:"column:id,primary_key" json:"id"`
	// CreatedAt is set on INSERT and never updated. Stored in UTC.
	CreatedAt time.Time `ogon:"column:created_at,index" json:"createdAt"`
	// UpdatedAt is bumped on every UPDATE. Stored in UTC.
	UpdatedAt time.Time `ogon:"column:updated_at" json:"updatedAt"`
	// DeletedAt is the soft-delete marker. NULL = live; non-NULL = deleted.
	// Queries exclude deleted rows unless the caller opts in via Unscoped().
	DeletedAt *time.Time `ogon:"column:deleted_at,index" json:"deletedAt,omitempty"`
}

// IsDeleted reports whether the row has been soft-deleted.
func (b BaseModel) IsDeleted() bool { return b.DeletedAt != nil }

// PrimaryKey returns the model's primary key value. Used by the query
// builder's WHERE id = ? shorthand and the preload resolver.
func (b BaseModel) PrimaryKey() types.UUID { return b.ID }

// Touch bumps UpdatedAt to now. Caller is responsible for persisting.
func (b *BaseModel) Touch() { b.UpdatedAt = time.Now().UTC() }

// MarkDeleted sets DeletedAt to now (UTC). Caller persists.
func (b *BaseModel) MarkDeleted() {
	now := time.Now().UTC()
	b.DeletedAt = &now
}

// Restore clears the soft-delete marker.
func (b *BaseModel) Restore() { b.DeletedAt = nil }
