// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.

package record

import (
	"testing"
)

func TestParseTags_BasicClauses(t *testing.T) {
	tag := "column:email,type:text,unique,email"
	p, err := ParseTags(tag)
	if err != nil {
		t.Fatalf("ParseTags: %v", err)
	}
	if p.Column != "email" {
		t.Errorf("Column = %q, want email", p.Column)
	}
	if p.Type != "text" {
		t.Errorf("Type = %q, want text", p.Type)
	}
	if !p.Unique {
		t.Error("Unique not set")
	}
	if len(p.Validators) != 1 || p.Validators[0] != "email" {
		t.Errorf("Validators = %v, want [email]", p.Validators)
	}
}

func TestParseTags_FK(t *testing.T) {
	tag := "fk:orgs.id:cascade"
	p, err := ParseTags(tag)
	if err != nil {
		t.Fatalf("ParseTags: %v", err)
	}
	if p.ForeignKey == nil {
		t.Fatal("ForeignKey nil")
	}
	if p.ForeignKey.Table != "orgs" || p.ForeignKey.Column != "id" {
		t.Errorf("FK ref = %s.%s, want orgs.id", p.ForeignKey.Table, p.ForeignKey.Column)
	}
	if p.ForeignKey.OnDelete != FKCascade {
		t.Errorf("OnDelete = %q, want CASCADE", p.ForeignKey.OnDelete)
	}
}

func TestParseTags_TenantImpliesRLS(t *testing.T) {
	p, err := ParseTags("tenant_id,index")
	if err != nil {
		t.Fatalf("ParseTags: %v", err)
	}
	if !p.TenantID {
		t.Error("TenantID not set")
	}
	if !p.RLS {
		t.Error("tenant_id did not imply rls")
	}
}

func TestParseTags_Classify(t *testing.T) {
	p, _ := ParseTags("classify:pii")
	if p.Classify != ClassifyPII {
		t.Errorf("Classify = %q, want pii", p.Classify)
	}
	if _, err := ParseTags("classify:secret-data"); err == nil {
		t.Error("expected error on bad classify")
	}
}

func TestParseTags_UnknownClause(t *testing.T) {
	if _, err := ParseTags("index,bogus:1"); err == nil {
		t.Fatal("expected error on unknown clause")
	}
}

func TestParseTags_Empty(t *testing.T) {
	p, err := ParseTags("")
	if err != nil {
		t.Fatalf("ParseTags empty: %v", err)
	}
	if p.Column != "" || p.Index {
		t.Error("empty tag produced non-zero fields")
	}
}

func TestParseTags_PartialAndCheck(t *testing.T) {
	p, err := ParseTags("index,partial:deleted_at IS NULL,check:length(email) > 3")
	if err != nil {
		t.Fatalf("ParseTags: %v", err)
	}
	if p.Partial != "deleted_at IS NULL" {
		t.Errorf("Partial = %q", p.Partial)
	}
	if p.Check != "length(email) > 3" {
		t.Errorf("Check = %q", p.Check)
	}
}

func TestParseTags_FKBadRef(t *testing.T) {
	if _, err := ParseTags("fk:badref"); err == nil {
		t.Error("expected error on fk without table.col")
	}
}

func TestParseTags_JSONBEnumAudit(t *testing.T) {
	p, err := ParseTags("jsonb,enum,audit,nullable")
	if err != nil {
		t.Fatalf("ParseTags: %v", err)
	}
	if !p.JSONB || !p.Enum || !p.Audit || !p.Nullable {
		t.Errorf("missing flag: %+v", p)
	}
}

func TestParseTags_FKBadAction(t *testing.T) {
	if _, err := ParseTags("fk:orgs.id:on_delete:explode"); err == nil {
		t.Error("expected error on bad FK action")
	}
}
