# Segment

A Go + htmx starting template with real auth, persistence, and a **server-driven segment stack** — the browser already has most of the page, so each navigation only sends the part it doesn't. Layouts, their data, and the page title all fall out of _where a route lives_ instead of per-page code.

Built with:

- **Go 1.26** + [chi](https://github.com/go-chi/chi) router
- [templ](https://templ.guide) for type-safe, compiled HTML templates
- [htmx](https://htmx.org) + a splash of [Alpine.js](https://alpinejs.dev) for client interactivity
- **Postgres** via `pgx/v5` (users, migrations, seeds)
- **Redis** via `go-redis/v9` (sessions + segment data cache)
- [devenv](https://devenv.sh) for a reproducible dev environment

## What you get

- **Cookie auth** — Redis-backed sessions with an HttpOnly cookie, `RequireAuth` middleware, login/logout, and bcrypt password checking. A seeded `test@domain.com` / `password` user.
- **Postgres store** — versioned migrations (`schema_migrations`), a `users` table, and a `store.Users` repository.
- **Segment stack** — every layout is a _segment_ (stable ID + outlet + optional data loader). The server renders only the segments the browser doesn't have mounted yet, per request.
- **Cached segment data** — each segment's `Load` result is cached (in-memory by default, Redis in production via `seg.DefaultCache`), keyed per-session (`Scoped`) or global, with a `TTL`. Mutations invalidate via `seg.Invalidate` / `seg.InvalidateGlobal`.
- **Page modals** — the same URL serves a full page _or_ a modal via the `X-Modal` header (`seg.Modalable`), with expand-to-page, close/back, and Escape support.

## Layout

```
cmd/server/main.go        entrypoint: config, store, sessions, graceful shutdown
internal/
  auth/session.go         Redis-backed session manager + RequireAuth
  config/config.go        env-driven configuration
  store/                  Postgres pool, migrations, seed, users, Redis cache
  web/
    routes.go             wires segments onto chi routes (New(Deps))
    seg/seg.go            the segment-stack "framework" (middleware + renderer)
    domain/types.go       data returned by segment Load funcs
    layouts/              templ layouts: root, marketing, auth, dashboard, settings, modal
    pages/                templ pages: home, about, login, dashhome, settings, ...
```

## Quick start

```bash
# enter the devenv shell (Postgres + Redis are started and configured)
devenv shell

# run the server (migrates + seeds on boot, listens on :8080)
go run ./cmd/server

# open the app
#   http://localhost:8080
#   test@domain.com / password
```

> `devenv.yaml` / `devenv.nix` manage the whole toolchain (Go, templ, Tailwind, gopls) plus the Postgres and Redis services. `templ generate` regenerates the `*_templ.go` files after editing any `.templ` file.

## Configuration

All settings are env-driven (`internal/config/config.go`):

| Env var           | Default          | Purpose                          |
| ----------------- | ---------------- | -------------------------------- |
| `ADDR`            | `:8080`          | listen address                   |
| `DATABASE_URL`    | built from `PG*` | Postgres DSN (devenv sets `PG*`) |
| `REDIS_ADDR`      | `127.0.0.1:6379` | Redis address                    |
| `SESSION_NAME`    | `session`        | cookie name                      |
| `SESSION_TTL`     | `720h`           | session lifetime                 |
| `COOKIE_SECURE`   | `false`          | set `Secure` on the cookie       |
| `COOKIE_SAMESITE` | `lax`            | `lax` / `strict` / `none`        |
| `BCRYPT_COST`     | `10`             | password hash cost               |

## Docs

- **[docs/segments.md](docs/segments.md)** — the segment stack: why the page is a stack, why the URL decides everything, why data binds to mount, and why invalidation is explicit.
- **[docs/htmx-oob.md](docs/htmx-oob.md)** — out-of-band swaps: why fragment navigation leaves holes, and when (and when not) to reach for OOB.
- **[docs/adding-a-page.md](docs/adding-a-page.md)** — adding a page with the least code, and the thinking that makes it work.
