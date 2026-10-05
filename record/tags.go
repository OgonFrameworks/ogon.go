// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon:` tag grammar parser. The tag is a comma-separated list of clauses
// the migration engine, RLS generator, and query builder interpret. The
// grammar is intentionally small and unambiguous so that codegen can emit
// tags back from introspected schemas without round-trip drift.
//
// Grammar:
//
//      column:<name>            explicit column name
//      type:<sqltype>           explicit SQL type (overrides dialect default)
//      primary_key              this column is the PK (BaseModel.ID)
//      index                    create a plain btree index
//      unique                   unique constraint (single-column)
//      partial:<sql>            partial-index predicate, e.g. partial:deleted_at IS NULL
//      fk:<table.col>[:on_delete][:on_update]   foreign-key reference
//      check:<sql>              raw CHECK constraint
//      enum                     column is a Postgres enum (see types.Enum)
//      jsonb                    column is JSON/JSONB
//      rls                      column participates in RLS policies (per-tenant)
//      audit                    column is an audit metadata column
//      classify:<pii|secret|public>   data classification (DATA-093)
//      nullable                 column allows NULL
//      tenant_id                column is the per-tenant key (implies rls)
//      email|url|phone          validator hint (codegen emits validators)
//
// Unknown clauses are an error so typos surface at registration time rather
// than at the first migration run.

package record

import (
	"fmt"
	"strings"
)

// Classification labels how a column may be exposed across trust boundaries.
type Classification string

const (
	ClassifyPublic Classification = "public"
	ClassifyPII    Classification = "pii"
	ClassifySecret Classification = "secret"
)

// FKAction normalises SQL referential actions.
type FKAction string

const (
	FKNoAction   FKAction = "NO ACTION"
	FKCascade    FKAction = "CASCADE"
	FKSetNull    FKAction = "SET NULL"
	FKSetDefault FKAction = "SET DEFAULT"
	FKRestrict   FKAction = "RESTRICT"
)

// ForeignKey is a parsed `fk:` clause.
type ForeignKey struct {
	Table    string
	Column   string
	OnDelete FKAction
	OnUpdate FKAction
}

// Tags is the parsed representation of an `ogon:` struct tag.
type Tags struct {
	Column     string
	Type       string
	PrimaryKey bool
	Index      bool
	Unique     bool
	Partial    string
	ForeignKey *ForeignKey
	Check      string
	Enum       bool
	JSONB      bool
	RLS        bool
	Audit      bool
	TenantID   bool
	Classify   Classification
	Nullable   bool
	Validators []string // email|url|phone|...
}

// ParseTags parses an `ogon:` tag value. Unknown clauses produce an error
// (strict mode is the only mode). Whitespace is significant only inside
// `partial:` and `check:` clauses, where it is preserved verbatim so
// migration emission does not reflow SQL.
func ParseTags(tag string) (*Tags, error) {
	t := &Tags{Classify: ClassifyPublic}
	if tag == "" {
		return t, nil
	}
	parts := strings.Split(tag, ",")
	for _, raw := range parts {
		clause := strings.TrimSpace(raw)
		if clause == "" {
			continue
		}
		key, val, hasVal := clause, "", false
		if i := strings.IndexByte(clause, ':'); i >= 0 {
			key, val = clause[:i], clause[i+1:]
			hasVal = true
		}
		switch key {
		case "column":
			if !hasVal || val == "" {
				return nil, fmt.Errorf("ogon: column clause requires a name: %q", clause)
			}
			t.Column = val
		case "type":
			if !hasVal || val == "" {
				return nil, fmt.Errorf("ogon: type clause requires a value: %q", clause)
			}
			t.Type = val
		case "primary_key":
			t.PrimaryKey = true
		case "index":
			t.Index = true
		case "unique":
			t.Unique = true
		case "partial":
			if !hasVal || val == "" {
				return nil, fmt.Errorf("ogon: partial clause requires SQL predicate: %q", clause)
			}
			t.Partial = val
		case "fk":
			fk, err := parseFK(val)
			if err != nil {
				return nil, err
			}
			t.ForeignKey = fk
		case "check":
			if !hasVal || val == "" {
				return nil, fmt.Errorf("ogon: check clause requires SQL: %q", clause)
			}
			t.Check = val
		case "enum":
			t.Enum = true
		case "jsonb":
			t.JSONB = true
		case "rls":
			t.RLS = true
		case "audit":
			t.Audit = true
		case "tenant_id":
			t.TenantID = true
			t.RLS = true // tenant_id implies RLS by convention
		case "classify":
			if !hasVal {
				return nil, fmt.Errorf("ogon: classify clause requires pii|secret|public: %q", clause)
			}
			c := Classification(val)
			switch c {
			case ClassifyPublic, ClassifyPII, ClassifySecret:
				t.Classify = c
			default:
				return nil, fmt.Errorf("ogon: classify must be pii|secret|public, got %q", val)
			}
		case "nullable":
			t.Nullable = true
		case "email", "url", "phone", "uuid", "isbn", "credit_card":
			t.Validators = append(t.Validators, key)
		default:
			return nil, fmt.Errorf("ogon: unknown tag clause %q (check spelling)", key)
		}
	}
	return t, nil
}

func parseFK(val string) (*ForeignKey, error) {
	if val == "" {
		return nil, fmt.Errorf("ogon: fk clause requires table.col")
	}
	// split reference from actions: table.col:on_delete:on_update
	pieces := strings.Split(val, ":")
	if len(pieces) < 1 || pieces[0] == "" {
		return nil, fmt.Errorf("ogon: fk clause requires table.col")
	}
	ref := pieces[0]
	dot := strings.LastIndexByte(ref, '.')
	if dot < 0 || dot == len(ref)-1 {
		return nil, fmt.Errorf("ogon: fk reference must be table.col, got %q", ref)
	}
	fk := &ForeignKey{
		Table:    ref[:dot],
		Column:   ref[dot+1:],
		OnDelete: FKNoAction,
		OnUpdate: FKNoAction,
	}
	for _, p := range pieces[1:] {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		action := FKAction(strings.ToUpper(strings.ReplaceAll(p, " ", "_")))
		switch action {
		case FKNoAction, FKCascade, FKSetNull, FKSetDefault, FKRestrict:
			// first non-empty action goes to OnDelete, second to OnUpdate
			if fk.OnDelete == FKNoAction && action != FKNoAction {
				fk.OnDelete = action
			} else {
				fk.OnUpdate = action
			}
		default:
			return nil, fmt.Errorf("ogon: fk action %q not recognised", p)
		}
	}
	return fk, nil
}
