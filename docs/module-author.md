# Module author guide

> **Goal**: write an OgonGo module — a versioned, signed package that
> plugs into any OgonGo project via `ogon add`.

This page covers, in order: **what**, **when**, **quickstart**, **config**,
**test**, **prod**, **escape**, **troubleshoot** (DOC-018).

---

## What

An OgonGo module (Part XVI) is a Go module that:

- declares an `ogon.module.json` manifest with:
  - `name`, `version`, `framework_constraint` (semver range),
  - `provides` (routes, models, jobs, config keys),
  - `requires` (other modules, with version constraints);
- ships a `Code generated` marker on every generated file (so `ogon
  gen` knows it owns them);
- signs every release with a Sigstore key (the manifest carries the
  signature);
- is published to the module registry (`ogon modules publish`).

`ogon add <module>` downloads the module, verifies the signature,
checks the framework constraint, and registers it in
`ogon.yaml#modules`. `ogon remove <module>` warns about orphaned
references.

## When

- You want to ship a capability that is not first-party (Part XX):
  Kafka, NATS, RabbitMQ, SQS, MongoDB, Elasticsearch, MySQL, SAML,
  SCIM, Terraform for GCP/Azure, Cloudflare, Helm.
- You want to ship an integration (Stripe, Twilio, SendGrid).
- You want to share internal code across multiple OgonGo projects
  without forking.

## Quickstart

```bash
# 1. scaffold
ogon new ogon-kafka --template module
cd ogon-kafka

# 2. edit the manifest
$EDITOR ogon.module.json
# {
#   "name": "ogon-kafka",
#   "version": "0.1.0",
#   "framework_constraint": ">=1.0.0 <2.0.0",
#   "provides": {
#     "modules": ["kafka"],
#     "config_keys": ["kafka.brokers", "kafka.topic"]
#   },
#   "requires": []
# }

# 3. write the module (it's a plain Go package)
$EDITOR kafka.go

# 4. test
ogon test

# 5. sign and publish
ogon modules sign --key $OGON_SIGNING_KEY
ogon modules publish
```

In a consuming project:

```bash
ogon add ogon-kafka@0.1.0
```

## Config

The module's `ogon.module.json` is its config contract. The consuming
project's `ogon.yaml` adds the module's config keys:

```yaml
modules:
  - name: ogon-kafka
    version: 0.1.0

kafka:
  brokers: ["localhost:9092"]
  topic: events
```

## Test

The module ships its own tests. The consuming project's
`test.ModuleCompatTest` asserts the module satisfies its manifest:

```go
func TestModuleCompat(t *testing.T) {
    test.ModuleCompatTest(t, "ogon-kafka", "0.1.0")
}
```

AT-011: "Tiny project promoted to modular layout via `ogon gen module`
without code rewrite."

## Prod

- Publish signed releases; the registry rejects unsigned modules.
- Pin the framework constraint; do not over-constrain.
- Document the config keys in the module's `README.md` and in
  `ogon.module.json#provides.config_keys`.

## Escape

- **Local module**: drop a directory under `modules/` with an
  `ogon.module.json`; `ogon add <name>` will pick it up without
  publishing.
- **Unsigned module**: set `modules.allow_unsigned: ["<name>"]` in
  `ogon.yaml` (dev only; CI fails on unsigned modules in prod).

## Troubleshoot

| Symptom                                | Fix                                                            |
|----------------------------------------|----------------------------------------------------------------|
| `ogon add` rejects the signature        | The signing key changed; re-sign or pin the old key.           |
| `framework_constraint` mismatch         | Bump the constraint or upgrade the framework.                  |
| `ogon remove` warns about orphans       | Remove the references the warning lists; re-run.               |
| Module's config keys collide with core  | Rename them; the registry rejects collisions at publish time.  |

---

See also: [Reference: modules](./reference/modules.md),
[Architecture](./architecture.md).

<!-- MIT License — Copyright (c) 2026 OgonFrameworks. All rights reserved. -->
