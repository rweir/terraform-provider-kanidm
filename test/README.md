# Acceptance test harness

`make test-acc-up` brings up a self-contained Kanidm instance in Docker,
bootstraps an admin account, mints a RW API token for the test SA, and
writes `test/.env`. `make test-acc` runs the acceptance-tagged Go tests
against it. `make test-acc-down` tears it down.

Tests are gated by both `TF_ACC=1` and a `//go:build acc` build tag so
they don't run as part of `go test ./...`.

## Quick start

```sh
make test-acc-up        # ~30s on first run, less on rebuilds
make test-acc           # runs ./internal/provider/*_acc_test.go
make test-acc-down      # clean up containers + data
```

`test-acc-up` will wipe `test/data/` so every run starts from a blank
database — acceptance tests are not supposed to reason about prior
state.

## What's inside

- `docker-compose.yaml` — single `kanidm/server` container, bound to
  `127.0.0.1:8443`.
- `bootstrap.sh` — generates a self-signed cert (10y), writes a
  `server.toml`, brings up the container, recovers the `admin`
  password, creates and elevates a `tofu-test` service account, mints
  a RW API token, writes `test/.env`.
- `data/` (gitignored) — runtime state: certs, server config, sqlite
  database.

## Why not testcontainers-go?

Less moving parts. The compose file + bootstrap script is something
you can read and debug with `docker compose logs kanidm` and
`docker exec -it kanidm-acctest bash`. testcontainers hides that.

If we ever want per-test isolation rather than per-suite, we'd revisit
— but acceptance tests for this provider create randomly-named
resources and clean them up, so a single shared instance is enough.
