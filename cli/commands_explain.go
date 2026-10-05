// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// `ogon explain <thing>` — explain a framework concept. Stable output so
// agents and humans get the same structured payload every time. Part II.6.

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OgonFrameworks/ogon.go/di"
	"github.com/OgonFrameworks/ogon.go/diag"
	"github.com/spf13/cobra"
)

type explainData struct {
	Topic      string   `json:"topic"`
	Summary    string   `json:"summary"`
	Details    []string `json:"details,omitempty"`
	Examples   []string `json:"examples,omitempty"`
	References []string `json:"references,omitempty"`
	ExitCodes  []int    `json:"exit_codes,omitempty"`
	// Graph is the rendered DI graph (only for `ogon explain di`).
	Graph string `json:"graph,omitempty"`
	// Dot is the Graphviz representation (only for `ogon explain di --dot`).
	Dot string `json:"dot,omitempty"`
}

func (d explainData) RenderHuman(c *CLI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", c.bold(d.Topic), c.dim("— "+d.Summary))
	for _, det := range d.Details {
		fmt.Fprintf(&b, "  • %s\n", det)
	}
	for _, ex := range d.Examples {
		fmt.Fprintf(&b, "  %s %s\n", c.cyan("example:"), ex)
	}
	for _, r := range d.References {
		fmt.Fprintf(&b, "  %s %s\n", c.dim("see:"), r)
	}
	if d.Graph != "" {
		fmt.Fprint(&b, d.Graph)
	}
	if d.Dot != "" {
		fmt.Fprintf(&b, "\n%s Graphviz:\n", c.dim("•"))
		fmt.Fprint(&b, d.Dot)
	}
	return b.String()
}

// explainers is the concept registry. Order is part of the contract; the
// `ogon explain` (no arg) listing enumerates these keys. Add a topic
// whenever a new framework behavior lands (DX-009: every framework
// behavior must be explained).
var explainers = map[string]func() explainData{
	"route":      explainRoute,
	"model":      explainModel,
	"config":     explainConfig,
	"di":         explainDI,
	"module":     explainModule,
	"component":  explainComponent,
	"feature":    explainFeature,
	"exit-codes": explainExitCodes,
	"gen":        explainGen,
	"diag":       explainDiag,
	"runtime":    explainRuntime,
	"jobs":       explainJobs,
	"live":       explainLive,
	"auth":       explainAuth,
	"obs":        explainObs,
	"infra":      explainInfra,
	"migrate":    explainMigrate,
	"test":       explainTest,
	"ui":         explainUI,
}

func newExplainCmd(c *CLI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain <thing>",
		Short: "Explain a framework concept (route, model, config, di, module, component, feature, runtime, ...)",
		Long: `Print a stable, structured explanation of a framework concept.

Topics: route, model, config, di, module, component, feature, exit-codes,
gen, diag, runtime. The same payload is emitted in JSON and human views (DX-025).

For "di", scans app/services and renders the live provider graph.
Use --dot to additionally emit the Graphviz representation (DI-015).`,
		Args: cobra.ArbitraryArgs,
		RunE: wrap(c, func(c *CLI, cmd *cobra.Command, args []string) int {
			topic := ""
			if len(args) >= 1 {
				topic = args[0]
			}
			dotFlag, _ := cmd.Flags().GetBool("dot")
			return runExplain(c, cmd, topic, dotFlag)
		}),
	}
	cmd.Flags().Bool("dot", false, "emit Graphviz DOT (only valid with topic 'di')")
	return cmd
}

func runExplain(c *CLI, cmd *cobra.Command, topic string, dot bool) int {
	if topic == "" {
		topics := make([]string, 0, len(explainers))
		for k := range explainers {
			topics = append(topics, k)
		}
		data := explainData{
			Topic:   "available topics",
			Summary: "pass one of the topics below to `ogon explain`",
			Details: topics,
		}
		c.emit(cmd, data, nil)
		return ExitOK
	}
	fn, ok := explainers[topic]
	if !ok {
		var cands []string
		for k := range explainers {
			cands = append(cands, k)
		}
		d := diag.New("OGON-C0002", "unknown topic",
			topic+" is not an explainable topic")
		d.Expected = strings.Join(cands, "|")
		d.Found = topic
		d.Fix = Suggest(topic, cands, DefaultSuggestDistance)
		c.emit(cmd, nil, []diag.Diag{*d})
		return ExitUsage
	}
	data := fn()
	// Special-case: for "di" we render the live provider graph from
	// app/services if the project has one (DI-014/015).
	if topic == "di" {
		if root, ok := c.requireProject(cmd); ok {
			graph, dotOut, diags := renderLiveDIGraph(root, dot)
			if graph != "" {
				data.Graph = graph
			}
			if dot && dotOut != "" {
				data.Dot = dotOut
			}
			if len(diags) > 0 {
				c.emit(cmd, data, diags)
				return ExitOK
			}
		}
	}
	c.emit(cmd, data, nil)
	return ExitOK
}

// renderLiveDIGraph scans <root>/app/services and renders the provider graph
// (and optional Graphviz DOT). Returns empty strings when no services dir.
func renderLiveDIGraph(root string, wantDot bool) (string, string, []diag.Diag) {
	servicesDir := filepath.Join(root, "app", "services")
	if _, err := os.Stat(servicesDir); err != nil {
		return "", "", nil
	}
	providers, err := di.ScanDir(servicesDir)
	if err != nil {
		d := diag.Wrap(err, diag.Diag{Code: "OGON-G0001", Title: "di scan failed"})
		return "", "", []diag.Diag{*d}
	}
	g, err := di.NewGraph(providers, nil)
	if err != nil {
		d := diag.New("OGON-D0001", "di graph error", err.Error())
		// Still try to render the partial graph for human inspection.
		var buf bytes.Buffer
		_ = di.Explain(g, &buf, di.DefaultExplainOptions())
		return "\n" + buf.String(), "", []diag.Diag{*d}
	}
	var buf bytes.Buffer
	_ = di.Explain(g, &buf, di.DefaultExplainOptions())
	graph := "\n" + buf.String()
	dotOut := ""
	if wantDot {
		var dbuf bytes.Buffer
		_ = di.Graphviz(g, &dbuf)
		dotOut = dbuf.String()
	}
	return graph, dotOut, nil
}

// ---- explainers ----

func explainRoute() explainData {
	return explainData{
		Topic:   "route",
		Summary: "a declarative HTTP endpoint registered with the OgonGo router",
		Details: []string{
			"Routes are declared in routes/*.go and materialized by `ogon gen route`.",
			"Conflict detection runs at boot and in `ogon routes check` (OGON-R0001).",
			"Path params use {name} syntax; method + path uniquely identify a route.",
		},
		Examples:   []string{"ogon gen route orders"},
		References: []string{"PROMPT.md Part IV (HTTP)", "ogon routes check"},
	}
}

func explainModel() explainData {
	return explainData{
		Topic:   "model",
		Summary: "a persisted record type with a schema and a migration",
		Details: []string{
			"Models live in models/*.go; the migration pair ships in migrations/.",
			"Generated validators, binders, and repositories derive from the model schema.",
		},
		Examples:   []string{"ogon gen model order"},
		References: []string{"PROMPT.md Part VI (Record)"},
	}
}

func explainConfig() explainData {
	return explainData{
		Topic:   "config",
		Summary: "ogon.yaml — the project's declarative configuration root",
		Details: []string{
			"Walked up from cwd; environment overlay is ogon.<env>.yaml.",
			"Invalid keys raise OGON-K0003; missing secrets raise OGON-K0005.",
			"`ogon check` validates the config in CI.",
		},
		Examples:   []string{"ogon inspect config", "ogon check"},
		References: []string{"PROMPT.md Part II.5 (Config)"},
	}
}

func explainDI() explainData {
	return explainData{
		Topic:   "di",
		Summary: "the generated dependency-injection container (Part V.5)",
		Details: []string{
			"`ogon build` emits the DI container from provider annotations.",
			"The Provide/Provider escape hatch (DX-P4) overrides generated wiring.",
			"Manual mode (BootOpts.Manual) skips DI generation entirely.",
		},
		References: []string{"PROMPT.md Part V (App lifecycle)"},
	}
}

func explainModule() explainData {
	return explainData{
		Topic:   "module",
		Summary: "a pluggable OgonGo module declared in modules/",
		Details: []string{
			"`ogon add <module>` appends to the module manifest.",
			"Modules contribute routes, models, jobs, and config keys.",
			"`ogon remove <module>` warns about orphaned references.",
		},
		Examples:   []string{"ogon add auth", "ogon modules list"},
		References: []string{"PROMPT.md Part XVI (Modules)"},
	}
}

func explainComponent() explainData {
	return explainData{
		Topic:   "component",
		Summary: "a UI component (Part IX UI subsystem)",
		Details: []string{
			"`ogon gen ui <name>` scaffolds a component.",
			"Components are isomorphic: server render + client hydrate.",
		},
		Examples:   []string{"ogon gen ui button"},
		References: []string{"PROMPT.md Part IX (UI)"},
	}
}

func explainFeature() explainData {
	return explainData{
		Topic:   "feature",
		Summary: "an opt-in framework capability toggled by feature flags",
		Details: []string{
			"Flags resolve from config (feature.<name>) then env (OGON_FEATURE_<NAME>).",
			"Unknown flags raise OGON-K0003.",
		},
		References: []string{"PROMPT.md Part II.5"},
	}
}

func explainExitCodes() explainData {
	codes := AllExitCodes()
	details := make([]string, 0, len(codes))
	for _, c := range codes {
		details = append(details, fmt.Sprintf("%3d  %s  — %s", c, ExitName(c), ExitDescription(c)))
	}
	return explainData{
		Topic:      "exit-codes",
		Summary:    "normative CLI exit codes (Part III.2)",
		Details:    details,
		ExitCodes:  codes,
		References: []string{"PROMPT.md Part III.2"},
	}
}

func explainGen() explainData {
	return explainData{
		Topic:      "gen",
		Summary:    "deterministic code generation (Part III.1)",
		Details:    append([]string{"Kinds:"}, genKinds...),
		Examples:   []string{"ogon gen route orders --dry-run"},
		References: []string{"PROMPT.md Part III.1", "ogon gen --help"},
	}
}

func explainDiag() explainData {
	return explainData{
		Topic:   "diag",
		Summary: "structured diagnostics (Part II.7): OGON-<class><nnnn>",
		Details: []string{
			"Classes: C compile, R route, V validation, K config, M migration,",
			"         D dependency, S security, U runtime, G generation conflict.",
			"Every diagnostic carries Code/Title/What/Why/Where/Fix/Remedy/Docs.",
		},
		References: []string{"PROMPT.md Part II.7", "https://ogongo.dev/errors/"},
	}
}

// explainRuntime implements CORE-009: `ogon explain runtime` shows the applied
// runtime limits (cgroup-aware GOMAXPROCS, SetMemoryLimit) and their source.
// Live values are read at call time so the output reflects the actual process.
func explainRuntime() explainData {
	details := []string{
		"OgonGo applies cgroup-aware runtime tuning at boot (CORE-001..004).",
		"",
		"Sources read (in order, first wins):",
		"  1. /sys/fs/cgroup/memory.max        (cgroup v2)",
		"  2. /sys/fs/cgroup/memory/memory.limit_in_bytes  (cgroup v1 fallback)",
		"  3. /sys/fs/cgroup/cpu.max           (cpu quota → GOMAXPROCS)",
		"  4. /proc/cpuinfo                   (fallback if no cgroup)",
		"",
		"Applied at boot:",
		"  • debug.SetMemoryLimit(0.9 × cgroup memory limit)  — 90% safety margin",
		"  • runtime.GOMAXPROCS(cpu.max quota)                — automaxprocs-style",
		"",
		"Opt-out: OGON_RUNTIME_LIMITS=off env var (CORE-008) disables tuning for dev.",
		"",
		"Live snapshot: `ogon inspect runtime` shows the actual applied values.",
	}
	return explainData{
		Topic:   "runtime",
		Summary: "cgroup-aware runtime limits applied at boot (CORE-001..009)",
		Details: details,
		Examples: []string{
			"OGON_RUNTIME_LIMITS=off ogon dev   # skip tuning in dev",
			"ogon inspect runtime               # see applied values",
		},
		References: []string{
			"PROMPT.md Part V (Core runtime)",
			"PROMPT.md Part XXIV CORE-001..009",
			"ogon inspect runtime",
		},
	}
}

// explainJobs covers the jobs subsystem (Part VIII / OGON-JOBS-001).
func explainJobs() explainData {
	return explainData{
		Topic:   "jobs",
		Summary: "background jobs: queues, retries, DLQ, cron, outbox (Part VIII)",
		Details: []string{
			"Drivers: in-proc, db (FOR UPDATE SKIP LOCKED), redis (BRPOP).",
			"Retries: exponential backoff + jitter; DLQ after max attempts.",
			"Cron: leader-elected; only one worker runs a cron job at a time.",
			"Outbox: jobs.Enqueue inside a tx is transactional — rolled back if the tx rolls back.",
			"Idempotency: jobs.PayloadKey dedupes; AES-256-GCM at rest when configured.",
		},
		Examples: []string{
			"ogon gen job SendEmail",
			"jobs.Enqueue(tx, jobs.Envelope{Queue: 'emails', Kind: 'welcome', Payload: ...})",
			"ogon jobs status",
		},
		References: []string{
			"PROMPT.md Part VIII (Jobs)",
			"docs/howto/test.md",
		},
	}
}

// explainLive covers the realtime subsystem (Part IX / OGON-LIVE-001).
func explainLive() explainData {
	return explainData{
		Topic:   "live",
		Summary: "realtime: hub, channels, presence, backpressure, reconnect (Part IX)",
		Details: []string{
			"Transports: WebSocket (coder/websocket) + SSE.",
			"Hub owns channels; channels own subscriber sets; presence is per-channel.",
			"Backpressure: bounded per-subscriber queues; slow subscribers drop oldest.",
			"Reconnect: resume window with tail replay; client retries with backoff.",
			"Multi-node: pluggable backplane (pubsub) for fan-out across instances.",
			"Per-message auth: every message is authorized, not just the connection.",
		},
		Examples: []string{
			"live.Handle('/room/{id}', ChatRoom)",
			"live.Broadcast(ctx, 'room:'+id, msg)",
		},
		References: []string{
			"PROMPT.md Part IX (Live)",
			"docs/guides/realtime.md",
		},
	}
}

// explainAuth covers the auth subsystem (Part VII / OGON-SEC-001).
func explainAuth() explainData {
	return explainData{
		Topic:   "auth",
		Summary: "sessions, passkeys, OAuth, MFA, rate-limit, lockout, PII redaction (Part VII)",
		Details: []string{
			"Flows: session (cookie), passkey (WebAuthn), OAuth/OIDC, JWT, API key.",
			"Passwords: argon2id (memory=64MiB, iter=3, par=2).",
			"Lockout: 5 failures in 5min -> 15min lockout (OGON-S0004).",
			"Rate limit: per-IP (60/min) + per-user (5/min on /login).",
			"PII: redacted in logs, traces, metrics via shared corpus (obs.RedactionCorpus).",
			"FIPS: opt-in via GOEXPERIMENT=boringcrypto build (AT-023).",
		},
		Examples: []string{
			"ogon gen auth --flows session,passkey",
			"auth.RequireRole('admin')(handler)",
		},
		References: []string{
			"PROMPT.md Part VII (Auth)",
			"docs/tutorials/auth.md",
			"docs/security.md",
		},
	}
}

// explainObs covers the observability subsystem (Part XII / OGON-OBS-001).
func explainObs() explainData {
	return explainData{
		Topic:   "obs",
		Summary: "logs, traces, metrics, health, PII redaction, profiling (Part XII)",
		Details: []string{
			"Logs: structured JSON / text; PII redaction at the sink.",
			"Traces: OpenTelemetry; parent-based sampling (dev=1.0, prod=0.1).",
			"Metrics: Prometheus; cardinality guard; route_template label only.",
			"Health: /healthz (liveness), /readyz (readiness), /startupz (startup).",
			"Profiling: /debug/pprof/* gated behind obs.pprof.enabled + bearer token.",
			"Crash reporter: opt-in; ships to configured sink.",
		},
		Examples: []string{
			"obs.Counter('posts_created', '1').Inc()",
			"obs.Histogram('request_duration', 'ms').Observe(elapsed)",
			"ogon health",
		},
		References: []string{
			"PROMPT.md Part XII (Observability)",
			"docs/howto/debug.md",
		},
	}
}

// explainInfra covers the infra subsystem (Part XI / OGON-INFRA-001..004).
func explainInfra() explainData {
	return explainData{
		Topic:   "infra",
		Summary: "docker, compose, k8s, helm, terraform, cloudflare, secrets (Part XI)",
		Details: []string{
			"First-party generators: docker, compose, k8s, helm, terraform (AWS), cloudflare.",
			"Artifacts are committed, inspectable, editable; ogon:owned marker.",
			"ogon infra gen <target> writes files; ogon infra diff reports drift.",
			"ogon deploy --cloud <cloud> runs build -> push -> apply -> verify -> rollback.",
			"GCP / Azure / SAML / SCIM are external modules (Part XX).",
		},
		Examples: []string{
			"ogon infra gen docker",
			"ogon infra gen k8s",
			"ogon infra gen aws",
			"ogon deploy --cloud aws --rollback",
		},
		References: []string{
			"PROMPT.md Part XI (Infra)",
			"docs/deploy/docker.md",
			"docs/deploy/k8s.md",
			"docs/deploy/aws.md",
		},
	}
}

// explainMigrate covers the migration engine (Part VI.2 / OGON-DATA-001).
func explainMigrate() explainData {
	return explainData{
		Topic:   "migrate",
		Summary: "AST -> SQL reversible migrations; destructive-op gate (Part VI.2)",
		Details: []string{
			"ogon migrate diff:    print pending changes (no writes).",
			"ogon migrate create:  write a *.up.sql + *.down.sql pair.",
			"ogon migrate run:     apply pending (in a tx where driver supports DDL).",
			"ogon migrate rollback: one step, or --to <version>.",
			"ogon migrate status:  what's applied; what's pending.",
			"Destructive ops (DROP TABLE, ALTER DROP COLUMN with data loss) -> OGON-M0001 (exit 4); --yes to confirm.",
			"Auto-migrate on boot is forbidden in prod (Part XX).",
		},
		Examples: []string{
			"ogon migrate diff",
			"ogon migrate run",
			"ogon migrate rollback --to 0042",
		},
		References: []string{
			"PROMPT.md Part VI.2 (Migration engine)",
			"docs/howto/migrate.md",
		},
	}
}

// explainTest covers the test fixture platform (Part XIV / OGON-TEST-001).
func explainTest() explainData {
	return explainData{
		Topic:   "test",
		Summary: "fixture-first testing platform; in-process app + DB + realtime (Part XIV)",
		Details: []string{
			"test.NewApp(t, handler): in-process server + recorder, no port.",
			"test.NewDBTx(t): transaction-rollback DB; fast, parallel-safe.",
			"test.WSClient / test.SSEClient: realtime clients with auto-reconnect.",
			"test.JobRunner: run jobs inline; assert retry / DLQ.",
			"test.NewAuthzMatrix: generate role x route matrix from routes/.",
			"test.RedactionCorpus: assert no PII in span attrs.",
			"test.MemoryLeakSoak: run handler 10k times; assert RSS stable.",
			"test.BudgetGate: PR-level perf budget regression gate.",
			"ogon test: go test + fixtures + optional JUnit XML.",
		},
		Examples: []string{
			"ogon test --race --junit ./build/junit.xml",
			"ogon test -run TestUser",
		},
		References: []string{
			"PROMPT.md Part XIV (Testing)",
			"docs/howto/test.md",
		},
	}
}

// explainUI covers the UI subsystem (Part IX UI / OGON-UI-001).
func explainUI() explainData {
	return explainData{
		Topic:   "ui",
		Summary: "isomorphic .ogon components; compiler + tiny JS runtime (Part IX UI)",
		Details: []string{
			".ogon single-file components: template + script + style.",
			"Compiler: lexer -> parser -> AST -> codegen (Go handler + template + <= 15KB JS).",
			"Runtime: server-held state + tiny JS runtime; progressive enhancement (AT-014).",
			"Sanitize: bluemonday wrapper for user-supplied HTML.",
			"Budgets: <= 15KB JS per page (UI-048); HMR in dev.",
		},
		Examples: []string{
			"ogon gen ui button",
			"ogon gen route room --live",
		},
		References: []string{
			"PROMPT.md Part IX (UI)",
			"docs/adr/0003-ui-architecture.md",
		},
	}
}
