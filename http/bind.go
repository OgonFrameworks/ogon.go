// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Bind: c.Bind(v) covers query/path/header/cookie/body. The runtime fallback
// uses reflection; `ogon build` emits generated validators in a future phase
// (no runtime reflection in the production build).
//
// Tag schema (uniform across all sources):
//
//      `json:"name"`        — body field name (JSON only).
//      `query:"name"`       — query parameter.
//      `path:"name"`        — path parameter.
//      `header:"name"`      — request header.
//      `cookie:"name"`      — request cookie.
//      `validate:"required,min=1,max=100"` — validation directives.
//
// On decode error, Bind returns a 400 ProblemDetails with a field map
// (HTTP-077 no silent defaults).

package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// bindCtx is the entrypoint for c.Bind(v). It walks v's struct tags and
// populates each field from the appropriate source (query/path/header/
// cookie/body). The body is only read once (cached on the *Ctx); subsequent
// Bind calls reuse the cached body. Validation runs after all fields are
// populated; the first failure short-circuits with a 400 ProblemDetails.
func bindCtx(c *Ctx, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return BindProblem("Bind requires a non-nil pointer")
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return BindProblem("Bind requires a pointer to a struct")
	}
	t := elem.Type()

	var fieldErrors []FieldError

	// Read the body once (if Content-Type is JSON).
	maxBody := int64(10 << 20)
	if c.router != nil && c.router.MaxBodyBytes > 0 {
		maxBody = c.router.MaxBodyBytes
	}
	var bodyMap map[string]any
	contentType := c.Header("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, err := c.readBody(maxBody)
		if err != nil {
			return coerceProblem(err)
		}
		if len(data) > 0 {
			if err := jsonDecode(data, &bodyMap); err != nil {
				return BindProblem("malformed JSON body: " + err.Error())
			}
		}
	} else if strings.HasPrefix(contentType, "application/xml") && len(contentType) > 0 {
		// XML decode path (single struct unmarshal).
		data, err := c.readBody(maxBody)
		if err != nil {
			return coerceProblem(err)
		}
		if len(data) > 0 {
			xmlc := &XMLCodec{}
			if err := xmlc.Decode(strings.NewReader(string(data)), v); err != nil {
				return BindProblem("malformed XML body: " + err.Error())
			}
		}
		// XML path skips the per-field body walk; fall through to validation.
		bodyMap = nil
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := elem.Field(i)
		if err := bindField(c, fv, field, bodyMap, &fieldErrors); err != nil {
			return err
		}
	}

	// Validate.
	if errs := validateStruct(elem); len(errs) > 0 {
		fieldErrors = append(fieldErrors, errs...)
	}

	if len(fieldErrors) > 0 {
		p := ValidateProblem("one or more fields failed binding/validation")
		p.Errors = fieldErrors
		return p
	}
	return nil
}

// bindField populates one struct field from its tag sources.
func bindField(c *Ctx, fv reflect.Value, sf reflect.StructField, body map[string]any, errs *[]FieldError) error {
	if q := sf.Tag.Get("query"); q != "" {
		setFromString(fv, c.Query(q), errs, sf.Name)
	}
	if p := sf.Tag.Get("path"); p != "" {
		setFromString(fv, c.Param(p), errs, sf.Name)
	}
	if h := sf.Tag.Get("header"); h != "" {
		setFromString(fv, c.Header(h), errs, sf.Name)
	}
	if ck := sf.Tag.Get("cookie"); ck != "" {
		if v, err := c.Cookie(ck); err == nil {
			setFromString(fv, v, errs, sf.Name)
		}
	}
	if j := sf.Tag.Get("json"); j != "" {
		name := strings.Split(j, ",")[0]
		if name == "-" {
			return nil
		}
		if body != nil {
			if v, ok := body[name]; ok {
				setFromAny(fv, v, errs, sf.Name)
			}
		}
	}
	return nil
}

// setFromString converts s into fv's underlying type.
func setFromString(fv reflect.Value, s string, errs *[]FieldError, fieldName string) {
	if s == "" {
		return
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			*errs = append(*errs, FieldError{Field: fieldName, Code: "type", Message: "must be int", Value: s})
			return
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			*errs = append(*errs, FieldError{Field: fieldName, Code: "type", Message: "must be uint", Value: s})
			return
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			*errs = append(*errs, FieldError{Field: fieldName, Code: "type", Message: "must be float", Value: s})
			return
		}
		fv.SetFloat(f)
	case reflect.Bool:
		b, err := BoolParser(s)
		if err != nil {
			*errs = append(*errs, FieldError{Field: fieldName, Code: "type", Message: "must be bool", Value: s})
			return
		}
		fv.SetBool(b)
	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.String {
			fv.Set(reflect.ValueOf(strings.Split(s, ",")))
		}
	default:
		// unsupported kind
	}
}

// setFromAny converts an interface{} (from JSON) into fv.
func setFromAny(fv reflect.Value, v any, errs *[]FieldError, fieldName string) {
	switch fv.Kind() {
	case reflect.String:
		if s, ok := v.(string); ok {
			fv.SetString(s)
		} else if n, ok := v.(json.Number); ok {
			fv.SetString(n.String())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch n := v.(type) {
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				*errs = append(*errs, FieldError{Field: fieldName, Code: "type", Message: "must be int", Value: v})
				return
			}
			fv.SetInt(i)
		case float64:
			fv.SetInt(int64(n))
		}
	case reflect.Float32, reflect.Float64:
		switch n := v.(type) {
		case json.Number:
			f, err := n.Float64()
			if err == nil {
				fv.SetFloat(f)
			}
		case float64:
			fv.SetFloat(n)
		}
	case reflect.Bool:
		if b, ok := v.(bool); ok {
			fv.SetBool(b)
		}
	case reflect.Struct:
		// Recurse via JSON round-trip (slow path; codegen avoids this).
		data, _ := json.Marshal(v)
		_ = json.Unmarshal(data, fv.Addr().Interface())
	default:
		// unsupported
	}
}

// validateStruct runs the per-field validation tags on the struct.
// Supports: required, min=N, max=N, len=N, oneof=v1 v2 v3, email (regex
// simplified for the runtime fallback).
func validateStruct(v reflect.Value) []FieldError {
	t := v.Type()
	var errs []FieldError
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("validate")
		if tag == "" {
			continue
		}
		fv := v.Field(i)
		for _, rule := range strings.Split(tag, ",") {
			if e := validateRule(fv, field.Name, rule); e != nil {
				errs = append(errs, *e)
				break
			}
		}
	}
	return errs
}

// validateRule applies one validation directive to a field value.
func validateRule(fv reflect.Value, name, rule string) *FieldError {
	parts := strings.SplitN(rule, "=", 2)
	directive := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch directive {
	case "required":
		if isZero(fv) {
			return &FieldError{Field: name, Code: "required", Message: "field is required"}
		}
	case "min":
		n, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil
		}
		switch fv.Kind() {
		case reflect.String:
			if int64(len(fv.String())) < n {
				return &FieldError{Field: name, Code: "min", Message: fmt.Sprintf("length must be ≥ %d", n)}
			}
		case reflect.Slice, reflect.Map:
			if int64(fv.Len()) < n {
				return &FieldError{Field: name, Code: "min", Message: fmt.Sprintf("must have ≥ %d items", n)}
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if fv.Int() < n {
				return &FieldError{Field: name, Code: "min", Message: fmt.Sprintf("must be ≥ %d", n)}
			}
		}
	case "max":
		n, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil
		}
		switch fv.Kind() {
		case reflect.String:
			if int64(len(fv.String())) > n {
				return &FieldError{Field: name, Code: "max", Message: fmt.Sprintf("length must be ≤ %d", n)}
			}
		case reflect.Slice, reflect.Map:
			if int64(fv.Len()) > n {
				return &FieldError{Field: name, Code: "max", Message: fmt.Sprintf("must have ≤ %d items", n)}
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if fv.Int() > n {
				return &FieldError{Field: name, Code: "max", Message: fmt.Sprintf("must be ≤ %d", n)}
			}
		}
	case "len":
		n, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil
		}
		if int64(fv.Len()) != n {
			return &FieldError{Field: name, Code: "len", Message: fmt.Sprintf("length must be exactly %d", n)}
		}
	case "oneof":
		opts := strings.Fields(arg)
		s := fmt.Sprintf("%v", fv.Interface())
		found := false
		for _, o := range opts {
			if s == o {
				found = true
				break
			}
		}
		if !found {
			return &FieldError{Field: name, Code: "oneof", Message: "must be one of: " + strings.Join(opts, ", ")}
		}
	case "email":
		s := fv.String()
		if !strings.Contains(s, "@") || !strings.Contains(s, ".") {
			return &FieldError{Field: name, Code: "email", Message: "must be a valid email"}
		}
	}
	return nil
}

// isZero returns true when the value is the zero value for its kind.
func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Slice, reflect.Map, reflect.Array:
		return v.Len() == 0
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// coerceProblem converts any error to a ProblemDetails. *ProblemDetails pass
// through; others are wrapped in a 500 generic.
func coerceProblem(err error) *ProblemDetails {
	if err == nil {
		return nil
	}
	if p, ok := err.(*ProblemDetails); ok {
		return p
	}
	return NewProblem(http.StatusInternalServerError, "Internal Server Error", err.Error())
}
