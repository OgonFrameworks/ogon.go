// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Tiny sprintf shim — keeps the compiler package dependency-free
// (no fmt import required for error messages).
//
// The supported verbs are %s, %d, %v, %x — enough for diagnostics
// and tests. Unknown verbs are rendered as the verb rune literally.

package compiler

import (
	"strconv"
	"strings"
)

// sprintf is a minimal printf clone. It deliberately rejects full
// fmt.Printf semantics to keep the compiler package import-light.
func sprintf(format string, args ...any) string {
	var b strings.Builder
	arg := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 >= len(format) {
			b.WriteByte(c)
			continue
		}
		i++
		verb := format[i]
		switch verb {
		case 's':
			if arg < len(args) {
				b.WriteString(toString(args[arg]))
				arg++
			} else {
				b.WriteString("%!s(MISSING)")
			}
		case 'd':
			if arg < len(args) {
				b.WriteString(itoa(toInt(args[arg])))
				arg++
			} else {
				b.WriteString("%!d(MISSING)")
			}
		case 'v':
			if arg < len(args) {
				b.WriteString(toAnyString(args[arg]))
				arg++
			} else {
				b.WriteString("%!v(MISSING)")
			}
		case 'x':
			if arg < len(args) {
				b.WriteString(strconv.FormatInt(int64(toInt(args[arg])), 16))
				arg++
			}
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(verb)
		}
	}
	return b.String()
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case error:
		return t.Error()
	case nil:
		return "<nil>"
	default:
		return toAnyString(v)
	}
}

func toAnyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return itoa(t)
	case int64:
		return itoa(int(t))
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return "<nil>"
	case error:
		return t.Error()
	default:
		return "<unknown>"
	}
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case int32:
		return int(t)
	case uint:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}
