# AGENTS.md

## Project

Local Web App Server is a macOS-only loopback HTTP host for independently
installed local web applications. It serves each app's declared static files
and proxies only that app's `/api/` routes to its Unix-domain-socket backend.

## Invariants

- Bind only to `127.0.0.1`; do not add LAN or internet exposure.
- Keep the host generic. It must not contain app-specific UI, state, or logic.
- Do not accept shell commands or arbitrary executable paths from HTTP.
- Serve only files below each manifest's `web_root`.
- Pass backend arguments directly to `os/exec`; never build a shell command.
- Isolate backend failures so one app cannot stop another app or the host.
- Preserve immediate SSE flushing and request cancellation propagation.
- Treat an explicit `shutdown_timeout_seconds: 0` as an unlimited graceful wait.
- Keep host UI styles separate from all installed app styles.

## Go

- Use the latest available Go 1.26 patch release.
- Prefer the standard library.
- Keep implementation code under `internal/` and the command under `cmd/`.
- Add or update tests in the same change as tested code.
- Run `gofmt -w .`, `go test ./...`, `go test -race ./...`, `go vet ./...`,
  and `go build ./...` before completion.
