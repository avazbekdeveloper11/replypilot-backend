# ReplyPilot Backend

Go + Gin + GORM + PostgreSQL + Redis + RabbitMQ, clean architecture. This
repo now ships TWO binaries, not one:

- **`cmd/api`** — the API service described in `docs/ARCHITECTURE.md` §1:
  dashboard-facing REST API, Instagram OAuth connect, Meta webhook
  ingestion. Publishes to RabbitMQ but does not consume from it.
- **`cmd/worker-ai`** — the AI reply pipeline worker: consumes `dm.received`
  off RabbitMQ, retrieves grounding context from the org's knowledge base
  (pgvector), generates a reply with Gemini, sends it back over Instagram,
  and records what happened. See that command's doc comment for the full
  flow and its honest limitations (heuristic confidence, no dead-letter
  queue). **Both must be running** for the product to actually reply to
  DMs — `cmd/api` alone ingests and stores messages but never answers them.
  DM-sending/notification workers beyond this one are still separate,
  not-yet-built binaries in the full system.

## Read this first: known limitations

This codebase was written in a sandboxed environment with **no access to
the Go toolchain or `go.dev`/`golang.org`/`storage.googleapis.com`** (all
blocked by network policy) and **no Docker/root access** to install one
another way. Every file was written by hand and cross-checked manually —
package names, import paths, method signatures, interface satisfaction —
but **`go build ./...` has not actually been run against this code.**
Before you trust it, run:

```bash
go mod tidy   # generates go.sum — see below
go build ./...
go vet ./...
```

and fix whatever that surfaces. This is not a hedge to avoid responsibility
for bugs — it's the honest state of verification, and skipping this step
before deploying would be a mistake.

Specific known gaps, not oversights glossed over:

1. **`go.sum` is not included.** It contains cryptographic checksums of
   every dependency, correctly generated only by `go mod tidy` /
   `go mod download` actually fetching each module. A hand-written or
   fabricated `go.sum` would look plausible and fail module verification —
   worse than omitting it. Run `make tidy` once after cloning.
2. **`docs/docs.go` is a placeholder**, not the real swaggo-generated spec.
   `swag init` needs the Go toolchain to parse the `@Summary`/`@Param`
   annotations already written throughout `internal/delivery/http/v1`.
   Run `make swagger` once, locally — it overwrites the placeholder with
   the full spec. Until then `/swagger/index.html` loads but shows an
   empty path list.
3. **Row-level security webhook-read path** (closed in Sprint 2):
   `InstagramAccountRepository.FindByIGUserID` has to resolve "which
   organization owns this Instagram account" from Meta's webhook payload
   *before* any tenant context exists. Migration `000003_webhook_read_policy`
   adds a permissive, `SELECT`-only RLS policy on `instagram_accounts` gated
   by the `app.webhook_lookup` GUC, and that method sets the GUC with
   `SET LOCAL` inside a transaction — so the elevated read is scoped to
   exactly that one query and auto-clears on commit. See the migration and
   the method comment for the full rationale.
4. **Password reset has no real email delivery.** `ForgotPassword` logs
   the reset link via `internal/platform/notify.LogNotifier` instead of
   emailing it — there is no SMTP/SES/Postmark integration in this
   codebase. Functionally the token flow (generate, store in Redis
   single-use, redeem, update password) is real; only the "send it to the
   user" step is a stand-in. Check server logs (`Warn` level) for the
   link in dev. Implement a real `notify.PasswordResetNotifier` before
   this goes near real users — see that file's doc comment.
5. **Password reset doesn't revoke other sessions.** Resetting a password
   doesn't invalidate refresh tokens issued before the reset — see the
   doc comment on `auth.UseCase.ResetPassword` for why (the refresh-token
   store isn't indexed by user today) and what closing it would take.
6. **Login-discovery RLS exception** (same category as #3, added
   alongside the auth pages): `TeamMemberRepository.ListByUserID` resolves
   "which organizations can this email log into" before any org is
   chosen. Migration `000004_member_email_lookup_policy` adds the same
   shape of permissive, GUC-gated, `SELECT`-only policy — this time on
   `team_members`. See that migration's comment for the full rationale.
7. **Dashboard's Notifications endpoint is honest, not fully featured.**
   `GET /v1/dashboard/notifications` returns unread conversations
   (`Conversation.UnreadCount`, already correct via webhook ingestion), not
   output from a dedicated notifications table/producer — that table
   (`notification_channels`) is schema-only. `GET /v1/dashboard/ai-performance`
   now DOES reflect real data once `cmd/worker-ai` is running and has sent
   at least one reply — see gap #8.
8. **AI reply confidence is a heuristic, not a model-reported value.**
   Gemini's `generateContent` API doesn't return a confidence score.
   `cmd/worker-ai` (via `internal/usecase/ai`) uses the top RAG-retrieved
   chunk's cosine similarity as a proxy: below a threshold (or an empty
   knowledge base), it hands the conversation to a human WITHOUT calling
   Gemini, rather than generating an ungrounded guess. A handoff decided
   this way — before generation — produces no `ai_responses` row (that
   table's `message_id` is a NOT NULL FK into an actual sent message), so
   `dashboard/ai-performance`'s `handoff_rate` undercounts these. See
   `internal/usecase/ai`'s package doc comment for the full reasoning.
9. **AI reply pipeline has no message-level idempotency guard.** RabbitMQ
   delivers at-least-once; a crash between sending the Instagram reply and
   persisting the outbound message row could, on redelivery, send a
   duplicate reply. Not solved here — see `ai.UseCase.HandleInboundMessage`'s
   doc comment. There is also no dead-letter queue yet
   (`internal/platform/queue.Consumer.Run`'s doc comment): a message that
   always fails to process will be requeued forever.
10. **Platform-admin "suspend organization" is a local access gate only.**
    It flips `organizations.status` to `suspended`, which `auth.UseCase.Login`
    and `.Refresh` now reject on — but it does NOT call Stripe to
    cancel/pause the org's subscription. An operator suspending a
    delinquent or abusive org still needs to handle billing separately in
    Stripe. It also isn't mid-session-instant: an access token issued
    before the suspension keeps working until it expires (same
    coarse-JWT tradeoff as everywhere else in this codebase) — only login
    and token refresh are checked.
11. **"Approximate MRR" in the admin stats is a labeled estimate, not a
    real revenue figure.** `subscriptions` doesn't persist which billing
    period (monthly/yearly) was actually chosen at Stripe Checkout — see
    `entity.Subscription`'s doc comment — so `GET /v1/admin/stats` sums
    every active/trialing subscription's plan at its *monthly* price,
    which overstates true MRR for anyone actually paying yearly. The
    `subscriptions_by_plan` breakdown alongside it is exact (a plain
    count), specifically so this can be sanity-checked rather than taken
    as ground truth.
12. **Instagram connect — real end-to-end state, and what's still not
    covered.** Until recently `GET /v1/instagram/callback` was nested
    under the `protected` (JWT-required) route group — Meta's OAuth
    redirect has no Authorization header, so every callback 401'd before
    the handler ran and the connect flow had never actually worked as
    deployed. Fixed: the callback is now its own public group (safety
    comes from the CSRF `state`, verified server-side — see
    `InstagramHandler.Callback`'s doc comment), and the redirect is
    proxied through the frontend (`web_app/replypilot-web/app/(dashboard)/instagram/callback`
    → its BFF route → this public endpoint) so the user lands on a real
    success/error page instead of raw JSON. The full flow is now real:
    connect (`POST /v1/instagram/connect`) → callback/token exchange →
    list (`GET /v1/instagram/accounts`) → disconnect
    (`DELETE /v1/instagram/accounts/:id`, soft-delete of ReplyPilot's own
    stored access only — does not revoke the grant at Instagram's end,
    see `OAuthUseCase.Disconnect`'s doc comment) → status detection
    (`internal/usecase/ai.handleSendFailure` flips an account to
    `expired`/`revoked` when a Graph API send fails with error code 190)
    → refresh (`cmd/token-refresh`, a run-once-and-exit job that renews
    any token within 10 days of its ~60-day expiry). What's still not
    covered: (a) `cmd/token-refresh` needs an external scheduler (cron / a
    Kubernetes CronJob) invoking it on a recurring basis — `docker compose up`
    runs it once at stack startup, which proves it works but is not a
    schedule; (b) only `SendMessage` (via the AI pipeline) and
    `RefreshLongLivedToken` (via the refresh job) check for Graph API
    error code 190 — `FetchProfile` and `SubscribeApp`, called during
    connect, do not flip account status on an auth failure, since a
    failure there surfaces synchronously to the user as a failed connect
    attempt instead of a silently-broken existing account.

## Platform admin

A ReplyPilot-staff-only capability, unrelated to any per-organization
role (owner/admin/agent/viewer) — `users.is_platform_admin`, a single
boolean, checked by `middleware.RequirePlatformAdmin` on the
`/v1/admin/*` route group (list every organization with member/plan
info, suspend/reactivate an org's access, platform-wide aggregate
stats). See migration `000008_platform_admin` for the RLS mechanism
(same GUC-gated permissive-policy pattern as the webhook-lookup
exceptions) and `internal/usecase/admin`'s package doc comment for the
authorization boundary.

There is deliberately no API to grant this — flipping it on a user is a
security-sensitive elevation, not something to expose as a self-serve
endpoint even to other admins. Do it directly in Postgres:

```sql
UPDATE users SET is_platform_admin = true WHERE email = 'you@replypilot.example';
```

That user's *next* login or token refresh will carry the elevated claim
(`jwtutil.Claims.IsPlatformAdmin`, re-derived from the DB at issue time,
not trusted from a stale token) — an already-active session won't
retroactively gain admin access until it re-authenticates.

## Platform settings (Gemini API key)

`platform_settings` (migration `000010_platform_settings`) is a tiny
key/value store for ReplyPilot-staff-configured, platform-wide secrets —
today just `gemini_api_key`, encrypted at rest with the same AES-256-GCM
envelope (`TOKEN_ENCRYPTION_KEY`) already used for Instagram tokens. A
platform admin sets or rotates it from the admin panel
(`GET`/`PUT /v1/admin/settings/gemini`, `internal/usecase/platformsettings`)
instead of editing `GEMINI_API_KEY` in `.env` and redeploying every
service that calls Gemini.

For that to actually take effect without a restart, both `cmd/api` and
`cmd/worker-ai` run `internal/platform/geminikey`'s poller: it checks
`platform_settings` every 30s and, if the key changed, calls
`geminiapi.Client.SetAPIKey` (mutex-protected — safe to call from a
different goroutine than the one making requests). `GEMINI_API_KEY` in
`.env` is still read at boot as the bootstrap value — a fresh install with
nothing set in the admin panel yet keeps working exactly as before this
existed. Once a key is set via the admin panel, it takes priority; there
is no "unset" operation (see `platformsettings.UseCase.SetGeminiAPIKey`'s
doc comment) — rolling back to the env-var value requires a restart.
`cmd/token-refresh` doesn't call Gemini at all, so it has no poller and
isn't affected either way.

The admin panel's `GET` endpoint never returns the key itself, only
whether one is configured and when it last changed — same "write-only"
principle as a password field, consistent with how
`InstagramAccountResponse` never includes the encrypted Instagram token.

## What's actually implemented (fully wired, not stubbed)

- **Auth**: register (creates org + Owner user + membership in one flow),
  login (email + password, scoped to one org), refresh (Redis-backed
  revocation via allowlisted JTIs), logout, organization lookup by email
  (`GET /v1/auth/organizations`, the login-flow discovery step), forgot/
  reset password (`POST /v1/auth/password/{forgot,reset}` — see known gaps
  #4–#5 for the email-delivery caveat).
- **Instagram OAuth connect**: authorization URL generation, code exchange,
  long-lived token exchange, profile fetch, AES-256-GCM token encryption at
  rest, webhook subscription, disconnect, and a public (CSRF-state-verified)
  callback route proxied through the frontend BFF — see known gap #12 for
  the full flow and what it doesn't cover.
- **Instagram token lifecycle**: `internal/usecase/ai.handleSendFailure`
  detects Meta's error code 190 on a failed send and flips the account to
  `expired`/`revoked`; `cmd/token-refresh` proactively renews any token
  within 10 days of expiry (needs an external scheduler in production —
  see known gap #12).
- **Meta webhook ingestion**: HMAC-SHA256 signature verification
  (`X-Hub-Signature-256`), subscription handshake, idempotent message
  persistence, conversation upsert, event publish to RabbitMQ.
- **Conversations/messages read API**: cursor-paginated list + get.
- **Dashboard read API**: `GET /v1/dashboard/{stats,timeseries,ai-performance,notifications}`
  — conversation counts by status, unread count, messages-today, connected
  Instagram accounts, average first-response time (trailing 30 days),
  daily conversation counts, AI response stats, and unread-conversation
  notifications. See known gaps #7–#9 for what "AI performance" and
  "notifications" honestly mean here today.
- **Team management**: list/invite/change-role/remove members, list
  assignable roles (`/v1/team/*`) — invite only works for an email with an
  existing account, see `usecase/team`'s doc comment.
- **Knowledge base**: upload (pasted text or a .txt/.md file), list, get,
  delete documents (`/v1/knowledge-base/documents*`). Upload chunks the
  text, embeds every chunk via Gemini's `text-embedding-004` (768-dim,
  pgvector/HNSW), and writes it — synchronously, no background job queue
  yet (see `usecase/knowledgebase`'s doc comment).
- **AI reply pipeline** (`cmd/worker-ai`, `internal/usecase/ai`): consumes
  inbound DMs, does RAG retrieval against the knowledge base, generates a
  reply with Gemini (`gemini-2.0-flash`), sends it back over Instagram
  (`internal/integration/metaapi.Client.SendMessage`), and records an
  `ai_responses` row + citations. See known gaps #8–#9 for its heuristic
  confidence gate and idempotency caveat.
- **Platform settings** (`GET`/`PUT /v1/admin/settings/gemini`,
  `internal/usecase/platformsettings`): lets a platform admin set/rotate
  the Gemini API key from the admin panel instead of `.env` + redeploy —
  see the "Platform settings" section above for how the change actually
  reaches both `cmd/api` and `cmd/worker-ai` without a restart.
- **Conversation take-over** (`PATCH /v1/conversations/{id}/take-over`):
  claims a `pending_human` conversation for the calling user — the action
  behind the AI Inbox's "Take over" button. Only valid from
  `pending_human`; see `usecase/conversation.UseCase.TakeOver`'s doc
  comment.
- **Billing** (`/v1/billing/*`, `/webhooks/stripe`): list active plans,
  read the org's current subscription, create a Stripe Checkout session to
  subscribe, create a Stripe Billing Portal session to manage/cancel, and
  a webhook that keeps the local `subscriptions` row in sync with Stripe.
  Self-serve subscribe + Stripe-hosted management, not a custom in-app
  billing UI — no card entry, no in-app invoice list. See
  `usecase/billing`'s package doc comment for the full scope reasoning,
  and migration `000006`'s comment: `plans.stripe_price_id_*` needs real
  Stripe price ids set per-environment before Checkout can actually work.
- **Analytics** (`/v1/analytics/*`): response-time trend, AI usage/token
  trend, and conversation-outcome breakdown — three real aggregate queries
  over data the conversation/message/AI-response tables already produce.
  No lead-conversion-funnel analytics yet — there's no Leads feature to
  aggregate.
- **Settings/profile** (`/v1/users/me*`, `PATCH /v1/organizations/me`): a
  user's own profile (name, avatar URL, password change — email is
  deliberately not editable here, see `usecase/user`'s doc comment) and
  an organization's name/timezone (slug is deliberately not editable
  here, see `usecase/organization.UseCase.UpdateSettings`'s doc comment).
- **Platform admin** (`/v1/admin/*`): list every organization with
  member/plan aggregates, suspend/reactivate an org's login access,
  platform-wide stats (org/user/conversation/message counts, active
  subscriptions, approximate MRR). Gated by `RequirePlatformAdmin`
  middleware off a single `users.is_platform_admin` boolean — see the
  "Platform admin" section above for the RLS mechanism and how to grant
  it, and known gaps #10–#11 for what "suspend" and "approximate MRR"
  honestly mean today.
- Every cross-cutting requirement from the original ask: Repository
  Pattern, constructor-injection DI (`internal/di`), JWT, webhook
  verification, Redis-backed distributed rate limiting, structured logging
  (zap), 12-factor config, Docker (multi-stage, distroless, non-root),
  Swagger annotations, request validation, middleware stack, centralized
  error handling, versioned routes (`/v1`).

## What's scaffolded but not built out

Nothing left at the entity → repository → usecase → handler level — every
domain in `database/schema.sql` that has a real use in the product today
is wired end to end (see above). The pattern below is kept for the next
one that comes along (a `leads` feature, for example, which several
"known gap" notes above call out as intentionally out of scope):

1. `internal/domain/entity/<name>.go` — plain struct, no framework tags.
2. `internal/domain/repository/<name>_repository.go` — interface.
3. `internal/repository/postgres/<name>_repository.go` — GORM model +
   implementation. Wrap every RLS-protected table's queries in
   `withTenant` (see `internal/repository/postgres/tenant.go`).
4. `internal/usecase/<name>/usecase.go` — business logic against the
   interface, not the implementation.
5. `internal/delivery/http/v1/<name>_handler.go` — Gin handler + DTOs,
   `@Summary`/`@Param` swaggo annotations.
6. Wire it in `internal/di/container.go` and add the route in
   `internal/delivery/http/router.go`.

## Running locally

```bash
cp .env.example .env   # fill in JWT_SECRET, TOKEN_ENCRYPTION_KEY, META_*, GEMINI_API_KEY
go mod tidy
make docker-up          # postgres + redis + rabbitmq, runs migrations, then api + worker-ai
```

`docker-up` runs the `migrate` service (applies all migrations) before `api`
and `worker-ai` start, so the database is always at the right schema
version first. `GEMINI_API_KEY` (get one at aistudio.google.com/apikey) is
NOT in `mustGetEnv` — the API still boots without it, but knowledge-base
upload and every AI reply will fail their own calls with a clear error
until it's set.

Or without Docker, once Postgres/Redis/RabbitMQ are running and `.env`
points at them:

```bash
make migrate-up   # applies internal/migrations/*.up.sql via cmd/migrate
make run          # cmd/api
make run-worker-ai  # cmd/worker-ai — run in a second terminal; without it, DMs are never answered
```

### Migrations

Schema is owned by versioned migrations in `internal/migrations/`, embedded
into the `cmd/migrate` binary — not by direct `schema.sql` application
anymore (`database/schema.sql` is kept as the human-readable reference and
the source the baseline migration was split from).

```bash
make migrate-up          # apply all pending
make migrate-down        # roll back one step
make migrate-version     # show current version + dirty flag
make migrate-force V=1   # recovery only: mark version after a crash
make migrate-create NAME=add_something   # scaffold the next migration pair
```

`000001` is the squashed baseline (full current schema); `000002` seeds
reference data (permissions, system roles, plans). Never edit a migration
that has already run anywhere — always add a new one.

Health check: `GET /healthz`. Swagger UI (after `make swagger`):
`/swagger/index.html`.

## Layout

```
cmd/api/                     API service entrypoint
cmd/worker-ai/                AI reply pipeline worker entrypoint (main.go + handler.go)
cmd/migrate/                  migration runner entrypoint
internal/
  config/                    env-based config, fails fast on missing secrets
  domain/
    entity/                  plain structs, zero framework dependency
    repository/               interfaces (ports) — the Repository Pattern
    apperror/                 one error type, mapped to HTTP status in one place
  usecase/                   business logic, depends on repository interfaces only
    ai/                       RAG retrieval + Gemini generation + send + record (cmd/worker-ai's usecase)
    knowledgebase/             chunk, embed, store, search
  repository/
    postgres/                 GORM implementations + withTenant (RLS) helper
    redis/                     refresh-token allowlist
  platform/
    queue/                     RabbitMQ connection + Publisher + Consumer
  integration/
    metaapi/                   Meta Graph API HTTP client (incl. SendMessage)
    geminiapi/                  Gemini embeddings + generation HTTP client
  delivery/http/
    middleware/                auth, rate limiter, logging, recovery/error handling, CORS
    v1/                        handlers + DTOs
    router.go, server.go
  di/                         composition root (cmd/api only — cmd/worker-ai wires its own
                               narrower set of dependencies directly in main.go)
pkg/
  jwtutil/                     access + refresh token issuing/parsing
  hash/                        bcrypt
  crypto/                      AES-256-GCM for tokens at rest
  signature/                   Meta webhook HMAC verification
deployments/docker/Dockerfile   builds api + migrate + worker-ai from one image
docker-compose.yml               api, worker-ai, migrate, postgres, redis, rabbitmq
```
