# Upstream Sync Rules

This repository is a fork of `QuantumNous/new-api`. It has diverged on purpose in the areas listed below, so upstream changes are merged deliberately: adopt what fits, adapt what touches a diverged area, ignore what targets removed code. `AGENTS.md` defines when this file must be read (see **Upstream sync (mandatory read gate)**). Rules use the same MUST / Do NOT wording as `AGENTS.md`.

## Remotes

- `origin` is this fork. `upstream` is `https://github.com/QuantumNous/new-api.git`; add it if missing (`git remote add upstream https://github.com/QuantumNous/new-api.git`).
- Sync by merging `upstream/main` into `main` with `git merge --no-ff upstream/main`. Do NOT rebase fork history onto upstream, and do NOT replace a sync with cherry-picks: merge commits keep each sync point visible and make the next merge base correct.
- Do NOT push a sync, or merge it into `main` from another branch, until the maintainer approves.

## Fork divergences

Each area below differs from upstream by design. Keep the fork's design when resolving changes in these areas.

| Area | This fork | Where |
|------|-----------|-------|
| Console | This repository has no console. The web console is the separate repository `Herta-Network/new-api-frontend`, deployed on a static host; it brings in upstream's `web/` by its own `.agents/rules/upstream-sync.md`. Removed here: `web/`, `//go:embed web/dist` and the Umami and Google Analytics injection in `main.go`, `middleware/frontend_static.go` (static files and the console's Content-Security-Policy), `middleware/cache.go`, `common/embed-file-system.go`, the static-file rate limit (`GLOBAL_STATIC_RATE_LIMIT*`), and the frontend steps of `Dockerfile`, `makefile`, and the workflows. `router/web-router.go` redirects console paths to `FRONTEND_BASE_URL` or the Frontend Address. | `router/web-router.go`, `main.go` |
| Database | PostgreSQL 18+ only; no SQLite or MySQL code, options, or tests. Tests run on real PostgreSQL through `pkg/pgtest`. Schema changes are versioned SQL migrations run by golang-migrate; startup never calls `AutoMigrate`. | `model/main.go`, `model/migrate.go`, `model/migrations/`, `common/database.go`, `pkg/pgtest/` |
| Payments | ppanel-style gateways: EPay, Stripe (one Stripe payment method type per method), Cryptomus, wallet balance. Creem, Waffo, Waffo Pancake, the go-epay SDK, `TopUp`, `SubscriptionOrder`, and the old top-up and subscription payment endpoints are removed. | `internal/module/payment/` |
| Currency | One site currency (`payment_setting.currency`) and one rate source (`currency.ResolveRate`: manual override, then a fresh live rate). `USDExchangeRate`, the CNY and CUSTOM quota display types, and the custom currency symbol and rate are removed. `/api/status` exposes `site_currency`; the console store uses `siteCurrency`, `siteCurrencySymbol`, `usdRate`, `quotaDisplayMode`. | `internal/module/currency/`, `controller/misc.go` |
| Console routes | User pages live under `/user/...` and admin pages under `/admin/...`, with redirects from the old root paths. Server-built console links (payment return, emails) point into `/user`. `/user` and `/admin` are reserved frontend paths for task plugins. | `pkg/jsplugin/routing.go` |
| Admin home | `/admin` is an operations overview backed by `/api/admin/console/*`. The older analytics tabs are detail pages. | `internal/module/console/` |
| Model pricing | Model metadata and pricing share one workspace under `/admin/models`. Prices are edited through a simple real-price form that writes billing expressions, with the expression editor as the advanced mode. The system-settings Model Pricing page and `ratio-settings-card.tsx` are removed. | new-api-frontend |
| Group pricing | The group pricing UI is rebuilt around a group list, advanced rules, and a price simulator. Group semantics and backend logic are unchanged. | new-api-frontend |
| Payment compliance | The compliance acknowledgement that gated payments, redemption codes, subscription plans, and invitation rewards is removed: no `payment_setting.compliance_*` settings, `POST /api/option/payment_compliance`, `middleware.PaymentCompliance`, or `RiskAcknowledgementDialog`, and those features are always available. | `controller/`, `internal/module/payment/` |
| Desktop | Electron is removed. | — |
| Modules | Fork-owned domains are modules under `internal/module/` with an arch-tested boundary; the rest keeps upstream's layout as the shared kernel. Modules write kernel tables only through `model/module_api.go`. | `internal/module/`, `internal/arch/`, `model/module_api.go` |
| Fork features | OpenCode Go channel (type 64), thinking-signature repair, 429 cooldown, Coding Plan usage, conversation session recognition (`internal/module/conversation`), fast GPT token counting in `tokenkit/`, per-user payment rate limits, SMTP sender parsing, log and `quota_data` I/O tuning. | various |

## Classify before merging

List the incoming commits (`git log --oneline $(git merge-base main upstream/main)..upstream/main`) and decide for each one before resolving anything:

- **Adopt as-is:** relay and channel adaptors, `relaykit/`, `tokenkit/`, task plugins (`pkg/jsplugin/`, `plugins/tasks/`), the billing engine and settlement, security fixes, dependency and toolchain bumps, bug fixes in shared code, and new i18n strings.
- **Adapt:** anything that touches a diverged area. Keep the upstream intent and express it in the fork's design, following the rules below.
- **Ignore:** everything under `web/`, which new-api-frontend brings in by its own sync rules, and changes that only touch removed code, such as the console-serving code listed under Console, SQLite or MySQL code paths and docs, Electron, Creem, Waffo, Waffo Pancake, the old top-up and subscription payment endpoints, the retired currency display options, `ratio-settings-card.tsx`, the old group editors, or the payment compliance acknowledgement. Ignore the code, not the behavior: if the fix applies to a fork equivalent, port it there.

## Resolving changes by area

- **Console:** keep `web/` deleted. Before committing the merge, run `git rm -r -q --ignore-unmatch web`: upstream's new `web/` files merge in without a conflict, and changed ones conflict as modify/delete. Drop the frontend steps upstream adds to `Dockerfile`, `makefile`, and `.github/workflows/`, and the frontend parts of README changes. Port an upstream change to the console page the backend served (analytics injection, its Content-Security-Policy) to new-api-frontend (`src/lib/static-hosting.ts`). An upstream change to `pkg/billingexpr/testdata/frontend_simulation.json` or `service/testdata/group_rules_cases.json` is adopted here and then copied with `bun run contracts:sync` in new-api-frontend.
- **Database:** keep only the PostgreSQL branch of any dialect switch. Remove references to `common.MainDatabaseType`, `common.SetDatabaseTypes`, `common.SQLitePath`, `DatabaseTypeSQLite`, and `DatabaseTypeMySQL`. Rewrite upstream SQLite/MySQL/PostgreSQL matrix tests as a single PostgreSQL test on the package database (`pgtest.Reset`), or on a fresh schema (`pgtest.NewSchema` / `pgtest.NewSchemaDB`) for migration tests. GORM may silently drop clauses on PostgreSQL that MySQL honors (for example `Limit` on `Delete`, or on `Count` without a subquery), so check the generated SQL of any such upstream query.
- **Schema changes:** upstream changes the schema through model fields and GORM tags, additions to the `AutoMigrate` list in `migrateDB`, and Go functions that reshape tables before `AutoMigrate`. Adapt them: keep the model and tag change, add a migration with `make migration` that creates the same schema (`TestMigrationsMatchModels` lists anything missing), add a new model to `schemaModels` in `model/migrate_test.go`, and do not bring in the `AutoMigrate` list entry or the reshaping function. Port a data backfill as SQL in the same migration, or as an idempotent startup step in `migrateDB` when it needs Go. Upstream tests that build an old upstream schema and upgrade it apply only through the baseline: start them from the baseline migrations, or drop the case.
- **Payments:** port upstream fixes to shared concepts (quota limits, subscriptions, top-up quota conversion) into `internal/module/payment/`. Follow the **Payments** rules in `AGENTS.md`. Never reintroduce removed providers or endpoints.
- **Currency:** translate old fields to the unified ones. Backend `operation_setting.USDExchangeRate` maps to `currency.ResolveRate`, and the console's mapping is in new-api-frontend's sync rules. This applies to upstream tests too.
- **Console routes:** server-built console links point into `/user` or `/admin`. For any new top-level console route upstream adds, update server-built links and `reservedRouteNamespaces` in `pkg/jsplugin/routing.go`.
- **Tests that assert removed UI:** drop only the assertion that targets the fork's intentional difference, and keep the rest of the test.
- **Modules:** upstream never changes `internal/module/`, but an upstream commit can touch the kernel functions `model/module_api.go` wraps (`creditTopUpQuota`, `getSubscriptionPlanByIdTx`, the user quota cache, `lockForUpdate`) or the kernel tables a module reads. Keep the wrappers' signatures and behavior in step when resolving it, and keep new fork code out of the kernel: follow **Modules** in `AGENTS.md`.
- **Conversation sessions:** `service/channel_affinity.go` (the `conversation_session` key source) and the OpenCode Go adaptor (`relay/channel/opencodego/`) read the recognized session through the kernel helper `service.ConversationSessionID`, which calls the resolver the conversation module's middleware stores under `constant.ContextKeyConversationSessionID`. An upstream change to those files must keep calling that helper; never import `internal/module/conversation` from them. Upstream routes that relay chat, responses, messages or Gemini requests keep `conversationhttp.ConversationSession()` before `Distribute()`.
- **Third-party notices:** when upstream bumps a dependency listed in `THIRD-PARTY-LICENSES.md`, update its version there.
- **Protected project information:** follow `AGENTS.md`; never alter it while resolving.

## After the merge

- Keep the merge commit to conflict resolution and the adaptations needed for it to build and pass tests. Put larger follow-ups, such as supporting a new upstream feature in a fork-only UI, in separate commits after the merge.
- Sync new-api-frontend to the same upstream commit in the same round, by its own sync rules, and report which upstream console changes depend on backend changes in this merge.
- Run the full verification:
  - `go build ./...` and `go vet ./...`
  - `GOWORK=off go build ./...` in `relaykit/` and `tokenkit/`
  - `TEST_POSTGRES_DSN=… make test`
  - when upstream changed models, migrations, or indexes, the PostgreSQL 18 fresh-and-upgrade verification from **Database compatibility** in `AGENTS.md`
- If an upstream change touches billing as defined in `AGENTS.md`, read `.agents/rules/billing.md` (and `pkg/billingexpr/expr.md` for expressions) before resolving it.
- Report the result to the maintainer:
  - the incoming commits grouped as adopted, adapted, and ignored, with the reason for each adaptation or omission
  - every conflict and how it was resolved
  - follow-up commits
  - the verification commands and their results

  Then wait for approval before pushing.
