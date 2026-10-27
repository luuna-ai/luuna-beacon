# Luuna Beacon development

This component is a maintained fork of an upstream project; provenance is in `UPSTREAM.md` and the original MIT notice in `LICENSE`.

Use Go 1.27.0. From this directory:

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
go build ./cmd/luuna-beacon ./cmd/lbc ./cmd/lb
```

Source is in `cmd/` and `internal/`; tests use Go and real temporary files. Keep idiomatic, well-tested Go, advisory locks, atomic writes, JSON output contracts, and secret redaction. Test changes at the appropriate public boundary.

Primary executable: luuna-beacon (lbc; compatibility alias lb). This component owns composable agent prompts and instructions. Missions owns tickets/plans; Atlas owns structured expertise and project memory. Project data lives in `.luuna/beacon`; legacy project directories are migrated automatically by `internal/paths`. Preserve configuration keys, record schemas, public contracts, and environment variables unless an explicit migration is designed and tested. Runtime policy and approval enforcement belong in Luuna, not in a rewrite of this component.

Do not load imported dogfooding stores, session histories, or upstream agent skills as Luuna work records. Do not create them as a development ritual. Installations must not alter the root Git hook configuration. This is a public Luuna component maintained in the luuna-ai/luuna-beacon repository. Packages remain unpublished; registry publication and upstream self-upgrades require a separate reviewed release process.

`go test ./...`, `go vet ./...`, and `go build ./cmd/luuna-beacon ./cmd/lbc ./cmd/lb` are the supported component validation surface. Luuna consumes exact Git commits through submodules; changes here do not authorize changes to Luuna runtime policy.

Do not commit or push unless explicitly requested. Report remaining work without automatically creating issues or synchronizing component state.
