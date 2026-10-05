// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Version attrs on spans: service.version, deployment.environment,
// build.revision, host.name. Implements OBS-033.

package obs

import (
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// versionAttrs returns the version + environment attributes for the
// resource attached to every span. (OBS-033)
func versionAttrs(cfg TracerConfig) []attribute.KeyValue {
	out := []attribute.KeyValue{}
	if cfg.ServiceVersion != "" {
		out = append(out, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	if cfg.Environment != "" {
		out = append(out, semconv.DeploymentEnvironment(cfg.Environment))
	}
	if h := Hostname(); h != "" {
		out = append(out, attribute.String("host.name", h))
	}
	out = append(out, attribute.String("ogon.framework", "ogon.go"))
	out = append(out, attribute.String("ogon.framework_version", frameworkVersionAttr))
	return out
}

// frameworkVersionAttr is the constant stamped on every span. Tests
// override via SetFrameworkVersionForTest. (OBS-033)
var frameworkVersionAttr = "1.0.0"

// SetFrameworkVersionForTest overrides the version attr for tests.
// (OBS-033)
func SetFrameworkVersionForTest(v string) {
	frameworkVersionAttr = v
}

// RevisionAttribute returns a single attribute.KeyValue carrying the
// build revision. Tests use this to assert ldflags-stamped revisions
// landed on every span. (OBS-033)
func RevisionAttribute(rev string) attribute.KeyValue {
	return attribute.String("build.revision", rev)
}
