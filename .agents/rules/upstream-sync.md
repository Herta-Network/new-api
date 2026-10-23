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
| Database | PostgreSQL 18+ only; no SQLite or MySQL code, options, or tests. Tests run on real PostgreSQL through `pkg/pgtest`. Schema changes are versioned SQL migrations run by golang-migrate; startup never calls `AutoMigrate`. | `model/main.go`, `model/migrate.go`, `model/migrations/`, `common/database.go`, `pkg/pgtest/` |
| Payments | ppanel-style gateways: EPay, Stripe (one Stripe payment method type per method), Cryptomus, wallet balance. Creem, Waffo, Waffo Pancake, the go-epay SDK, `TopUp`, `SubscriptionOrder`, and the old top-up and subscription payment endpoints are removed. | `service/payment/`, `service/payment/gateway/`, `model/payment_*.go`, `controller/payment*.go` |
| Currency | One site currency (`payment_setting.currency`) and one rate source (`payment.ResolveRate`: manual override, then a fresh live rate). `USDExchangeRate`, the CNY and CUSTOM quota display types, and the custom currency symbol and rate are removed. `/api/status` exposes `site_currency`; the web store uses `siteCurrency`, `siteCurrencySymbol`, `usdRate`, `quotaDisplayMode`. | `service/payment/rates.go`, `controller/misc.go`, `web/src/stores/system-config-store.ts`, `web/src/lib/currency.ts` |
| Console routes | User pages live under `/user/...` and admin pages under `/admin/...`, with redirects from the old root paths. Server-built console links (payment return, emails) point into `/user`. `/user` and `/admin` are reserved frontend paths for task plugins. | `web/src/routes/_authenticated/{user,admin}/`, `pkg/jsplugin/routing.go` |
| Admin home | `/admin` is an operations overview backed by `/api/admin/console/*`. The older analytics tabs are detail pages. | `controller/console.go`, `web/src/features/dashboard/` |
| Model pricing | Model metadata and pricing share one workspace under `/admin/models`. Prices are edited through a simple real-price form that writes billing expressions, with the expression editor as the advanced mode. The system-settings Model Pricing page and `ratio-settings-card.tsx` are removed. | `web/src/features/models/`, `web/src/features/model-pricing/` |
| Group pricing | The group pricing UI is rebuilt around a group list, advanced rules, and a price simulator. Group semantics and backend logic are unchanged. | `web/src/features/system-settings/billing/group-pricing/` |
| Desktop | Electron is removed. | — |
| Fork features | OpenCode Go channel (type 64), thinking-signature repair, 429 cooldown, Coding Plan usage, conversation session recognition (`pkg/convo`), fast GPT token counting in `tokenkit/`, per-user payment rate limits, SMTP sender parsing, log and `quota_data` I/O tuning. | various |

## Classify before merging

List the incoming commits (`git log --oneline $(git merge-base main upstream/main)..upstream/main`) and decide for each one before resolving anything:

- **Adopt as-is:** relay and channel adaptors, `relaykit/`, `tokenkit/`, task plugins (`pkg/jsplugin/`, `plugins/tasks/`), the billing engine and settlement, security fixes, dependency and toolchain bumps, bug fixes in shared code, and new i18n strings.
- **Adapt:** anything that touches a diverged area. Keep the upstream intent and express it in the fork's design, following the rules below.
- **Ignore:** changes that only touch removed code, such as SQLite or MySQL code paths and docs, Electron, Creem, Waffo, Waffo Pancake, the old top-up and subscription payment endpoints, the retired currency display options, `ratio-settings-card.tsx`, or the old group editors. Ignore the code, not the behavior: if the fix applies to a fork equivalent, port it there.

## Resolving changes by area

- **Database:** keep only the PostgreSQL branch of any dialect switch. Remove references to `common.MainDatabaseType`, `common.SetDatabaseTypes`, `common.SQLitePath`, `DatabaseTypeSQLite`, and `DatabaseTypeMySQL`. Rewrite upstream SQLite/MySQL/PostgreSQL matrix tests as a single PostgreSQL test on the package database (`pgtest.Reset`), or on a fresh schema (`pgtest.NewSchema` / `pgtest.NewSchemaDB`) for migration tests. GORM may silently drop clauses on PostgreSQL that MySQL honors (for example `Limit` on `Delete`, or on `Count` without a subquery), so check the generated SQL of any such upstream query.
- **Schema changes:** upstream changes the schema through model fields and GORM tags, additions to the `AutoMigrate` list in `migrateDB`, and Go functions that reshape tables before `AutoMigrate`. Adapt them: keep the model and tag change, add a migration with `make migration` that creates the same schema (`TestMigrationsMatchModels` lists anything missing), add a new model to `schemaModels` in `model/migrate_test.go`, and do not bring in the `AutoMigrate` list entry or the reshaping function. Port a data backfill as SQL in the same migration, or as an idempotent startup step in `migrateDB` when it needs Go. Upstream tests that build an old upstream schema and upgrade it apply only through the baseline: start them from the baseline migrations, or drop the case.
- **Payments:** port upstream fixes to shared concepts (quota limits, subscriptions, top-up quota conversion) into `service/payment/` and `model/payment_*.go`. Follow the **Payments** rules in `AGENTS.md`. Never reintroduce removed providers or endpoints.
- **Currency:** translate old fields to the unified ones. Old `quotaDisplayType: 'CNY'` with `usdExchangeRate` maps to `siteCurrency` / `siteCurrencySymbol` / `usdRate`, and backend `operation_setting.USDExchangeRate` maps to `payment.ResolveRate`. This applies to upstream tests too.
- **Console routes:** place new upstream pages under `/user` or `/admin` by audience. Keep old-path redirects. Update server-built links and the task-plugin reserved paths for any new top-level route.
- **Model and group pricing UI:** apply upstream changes to the model workspace, the simple pricing form, and the group pricing component instead of the removed files. For a new billing expression variable or cache category (for example `cc12h`), check that `parseSimplePricingExpr` round-trips it. Add a simple-form field when the variable has a plain per-1M price; an expression the form cannot show must open in the advanced editor unchanged. Verify billing semantics in the backend code, not from commit messages.
- **Tests that assert removed UI:** drop only the assertion that targets the fork's intentional difference, and keep the rest of the test.
- **Locale files:** upstream keeps keys sorted with `String.prototype.localeCompare`. Resolve conflicts at the key level, not line by line: a key deleted on either side stays deleted, a key added on either side is kept, and a one-sided value change wins. Write the files back in `localeCompare` order and keep the protected footer key in its escaped form (`footer.new\u0061pi.projectAttributionSuffix`). After resolving, `bun run i18n:sync` must report no missing or extra keys and leave the files unchanged. Insert new fork keys in sorted order too.
- **Third-party notices:** when upstream bumps a dependency listed in `THIRD-PARTY-LICENSES.md`, update its version there.
- **Protected project information:** follow `AGENTS.md`; never alter it while resolving.

## After the merge

- Keep the merge commit to conflict resolution and the adaptations needed for it to build and pass tests. Put larger follow-ups, such as supporting a new upstream feature in a fork-only UI, in separate commits after the merge.
- Run the full verification:
  - `go build ./...` and `go vet ./...`
  - `GOWORK=off go build ./...` in `relaykit/` and `tokenkit/`
  - `TEST_POSTGRES_DSN=… make test`
  - `bun run lint`, `typecheck`, `test`, `build`, and `format:check` in `web/`
  - when upstream changed models, migrations, or indexes, the PostgreSQL 18 fresh-and-upgrade verification from **Database compatibility** in `AGENTS.md`
- If an upstream change touches billing as defined in `AGENTS.md`, read `.agents/rules/billing.md` (and `pkg/billingexpr/expr.md` for expressions) before resolving it.
- Report the result to the maintainer:
  - the incoming commits grouped as adopted, adapted, and ignored, with the reason for each adaptation or omission
  - every conflict and how it was resolved
  - follow-up commits
  - the verification commands and their results

  Then wait for approval before pushing.
