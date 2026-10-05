// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// i18n + number/date l10n + locale-prefix routing (UI-027/028/072).
//
// Message catalogs are simple key → translation maps per locale.
// The compiler emits one catalog per locale; the runtime consults
// the catalog for the active locale (from cookie, Accept-Language,
// or path prefix). Locale-prefix routing is opt-in (UI-072).

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Catalog is a single locale's message table.
type Catalog struct {
	Locale  string              // e.g. "en-US", "fr-FR"
	Msgs    map[string]string   // key → translation
	Plurals map[string]PluralFn // key → plural rule
}

// PluralFn returns the correct plural form index for `n`. The CLDR
// plural rules vary by locale; the runtime supplies a default that
// matches English (one/other).
type PluralFn func(n int) int

// EnglishPlural returns 0 for n==1 ("one") and 1 otherwise ("other").
func EnglishPlural(n int) int {
	if n == 1 {
		return 0
	}
	return 1
}

// I18n is the runtime translator.
type I18n struct {
	mu         sync.RWMutex
	catalogs   map[string]*Catalog
	defaultLoc string
}

// NewI18n constructs an empty translator.
func NewI18n(defaultLocale string) *I18n {
	if defaultLocale == "" {
		defaultLocale = "en-US"
	}
	return &I18n{catalogs: map[string]*Catalog{}, defaultLoc: defaultLocale}
}

// Register adds a locale catalog.
func (i *I18n) Register(c *Catalog) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.catalogs[c.Locale] = c
}

// T returns the translation for `key` in `locale`, falling back to
// the default locale and then to the key itself.
func (i *I18n) T(locale, key string, args ...any) string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if c, ok := i.catalogs[locale]; ok {
		if s, ok := c.Msgs[key]; ok {
			return fmt.Sprintf(s, args...)
		}
	}
	if c, ok := i.catalogs[i.defaultLoc]; ok {
		if s, ok := c.Msgs[key]; ok {
			return fmt.Sprintf(s, args...)
		}
	}
	return key
}

// TN returns a pluralised translation. `n` selects the plural form.
func (i *I18n) TN(locale, key string, n int, args ...any) string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if c, ok := i.catalogs[locale]; ok {
		if fn, ok := c.Plurals[key]; ok {
			form := fn(n)
			suffixed := key + "." + pluralSuffix(form)
			if s, ok := c.Msgs[suffixed]; ok {
				return fmt.Sprintf(s, append([]any{n}, args...)...)
			}
		}
		if s, ok := c.Msgs[key]; ok {
			return fmt.Sprintf(s, append([]any{n}, args...)...)
		}
	}
	return key
}

// FormatNumber formats an integer using the supplied locale's
// grouping rules. The implementation uses English-style commas;
// production users should swap in golang.org/x/text/language for
// full CLDR support (UI-028).
func FormatNumber(locale string, n int64) string {
	// Group thousands with commas.
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre == 0 {
		pre = 3
	}
	b.WriteString(s[:pre])
	for i := pre; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	out := b.String()
	if neg {
		return "-" + out
	}
	return out
}

// FormatDate formats a time using a locale-aware pattern. The
// implementation falls back to RFC3339 when no locale is supplied
// (UI-028).
func FormatDate(locale string, t time.Time) string {
	switch {
	case strings.HasPrefix(locale, "en"):
		return t.Format("Jan 2, 2006")
	case strings.HasPrefix(locale, "fr"), strings.HasPrefix(locale, "de"):
		return t.Format("02/01/2006")
	default:
		return t.Format(time.RFC3339)
	}
}

// LocalePrefixRouter is the opt-in locale-prefix router (UI-072).
// It strips a `/locale` prefix and stashes the active locale in the
// request context for downstream handlers.
type LocalePrefixRouter struct {
	Locales []string // supported locale codes
	Next    http.Handler
}

func (l *LocalePrefixRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, loc := range l.Locales {
		prefix := "/" + loc
		if strings.HasPrefix(r.URL.Path, prefix+"/") || r.URL.Path == prefix {
			ctx := WithLocale(r.Context(), loc)
			// Strip the prefix for downstream handlers.
			r2 := r.Clone(ctx)
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			l.Next.ServeHTTP(w, r2)
			return
		}
	}
	l.Next.ServeHTTP(w, r)
}

type localeKey struct{}

// WithLocale stashes a locale on the request context.
func WithLocale(ctx context.Context, loc string) context.Context {
	return context.WithValue(ctx, localeKey{}, loc)
}

// LocaleFromContext returns the stashed locale (or "").
func LocaleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(localeKey{}).(string)
	return v
}

// pluralSuffix maps plural-form index to CLDR suffix.
func pluralSuffix(form int) string {
	switch form {
	case 0:
		return "one"
	case 1:
		return "other"
	case 2:
		return "few"
	case 3:
		return "many"
	}
	return "other"
}

// ErrMissingLocale is returned by tests that demand an explicit locale.
var ErrMissingLocale = errors.New("ogon/ui: missing locale")
