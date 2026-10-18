# Zupfnoter Desktop POC

Proof of concept for a Go desktop companion and local Codex App Server integration.

## Requirements
- Go 1.22+
- Codex CLI installed and available on PATH
- Run `codex login` once

## Run
```sh
go run ./cmd/assistant
```
Open http://127.0.0.1:4317. The companion starts Codex on demand.

## Build
```sh
go build -o zupfnoter-assistant ./cmd/assistant
```

## Scope
This is an experimental local-only prototype. It uses a simple browser UI, a Go HTTP bridge, Codex JSON-RPC over stdio and SSE streaming. It does not yet integrate into zupfnoter.de or provide a signed installer, guided login, protocol registration, automatic Codex updates, approval handling, or a production-grade pairing mechanism. Do not use it for sensitive documents.
