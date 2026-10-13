# AF-12 — Account self-service

Implementation baseline: `7e7a6c7ccfc48a6b4286c3c732325eae5dd2df17` plus this working-tree change. Identity owns the data and transactions; Notification sends mail through the existing protected delivery-reference contract. No new service or secret is introduced.

## Deployment and configuration

1. Back up Identity and rehearse restore on staging. Apply additive migration `000008_account_self_service.up.sql` before starting the new Identity binary, even with the feature disabled. Existing users start at profile/auth version 1; their verification status is preserved.
2. Deploy Notification with support for `email_change` and `email_changed` before enabling Identity's producer. Its existing SMTP and delivery-key configuration remains in use. An old Notification binary would send the wrong template for these kinds and is not a compatible rollback target while deliveries remain.
3. Configure Identity using the names below. Keep the feature off until staging SMTP, HTTPS, trusted proxy/IP limiting, the real gateway, alerts and a backup restore have been verified. No legacy account is forced to verify to log in, shop or fulfill existing orders.
4. Deploy the frontend. `/account` and its navigation entry depend on the authenticated capability API, not a frontend environment flag. Pilot all three roles, then enable the feature deliberately.

| Variable | Default / requirement |
| --- | --- |
| `FEATURE_ACCOUNT_SELF_SERVICE_ENABLED` | `false`; enables self-service and existing email verification delivery |
| `EMAIL_VERIFY_TTL` | `30m`; accepts 1m–30m; applies to verification and email-change links |
| `ACCOUNT_TOKEN_RETENTION` | `24h`; accepts 1h–720h; expiry plus this interval before token/history purge |
| `IDENTITY_EMAIL_CHANGE_URL` | `/change-email` on the frontend's absolute URL; HTTPS in staging/production, no query or fragment |
| `IDENTITY_EMAIL_VERIFY_URL` | Existing `/verify-email` URL; required when either verification or self-service is on |

`FEATURE_EMAIL_VERIFICATION_ENABLED` continues to work independently. Either flag permits current-address verification. Enabling AF-12 also queues the initial verification at registration. Confirming a new email verifies it. Existing capability checks such as staff acceptance keep their own policy; AF-12 does not enable MFA or impose new shopping restrictions.

## Public contracts

All responses use the existing `data`/`error` envelope and `Cache-Control: no-store`. Mutations require an allowed Origin and `X-CSRF-Protection: 1`; authenticated operations use the current session's identity. New account DTOs reject unknown properties including role, user ID and email in a profile update. Password bodies are never logged.

| Route | Contract |
| --- | --- |
| `GET /api/auth/account-capabilities` | `{account_self_service}`; authentication required |
| `GET /api/auth/me` | Safe user including `phone`, `profile_version`, `email_verified`; no auth version or hashes |
| `PATCH /api/auth/me` | `{full_name,phone,expected_version}`; full name required, phone may be empty; stale version 409 |
| `POST /api/auth/password-changes` | `{current_password,new_password}`; success clears refresh cookie and requires sign-in |
| `POST /api/auth/email-verifications` | Existing resend; 60 seconds between requests, at most five links in a rolling hour per user, including registration |
| `POST /api/auth/email-verifications/confirm` | `{token}`; existing `/confirmations` alias remains compatible |
| `POST /api/auth/email-changes` | `{new_email,current_password}`; 202 queued receipt; same per-user timing limits as verification |
| `POST /api/auth/email-changes/confirm` | `{token}`; confirms address, revokes all sessions, clears cookie, queues notice to previous address |
| `GET /api/auth/sessions` | `limit=20` (max 100), opaque `cursor`; `{items,next_cursor?}` |
| `DELETE /api/auth/sessions/:id` | Idempotent revoke scoped to caller; does not reveal or affect another user's session |
| `POST /api/auth/session-revocations` | `{scope:"all"}`; revokes all sessions including current |

Session metadata contains first issuance and latest refresh time, plus `current`. It is an approximation, not a device/activity tracker. Full IP addresses and user-agent strings are not collected or returned. The session family identifier is a revocation reference, not a credential.

New field/business validation returns 422 with a field-prefixed message; malformed/unknown JSON fields return 400. Verification confirmation errors and email-change conflicts/expiry/replay use generic 400 responses. Email-change requests do not probe account existence; normalized uniqueness is enforced only during confirmation, returning the same generic error as other unusable changes. An existing address therefore cannot be taken over by knowing its spelling.

## Transactions, delivery and rollback

Profile writes use CAS and audit in one transaction. Password comparison/hash generation runs outside the transaction; Identity rechecks the locked user, auth version and active session before writing. Password changes/reset increase auth version and revoke every refresh family; access-token introspection refuses those sessions on the next request.

Pending email changes live in `email_change_operations` with a token hash, encrypted target, captured auth version and expiry. A replacement invalidates the previous link. Confirmation locks the user before rereading the operation, consumes once, changes email, increments auth/profile versions, invalidates old reset/verification material, revokes sessions, queues the previous-address notice and audits in one transaction. Audit/queue failures roll the entire change back. Two users confirming the same normalized address cannot both succeed.

Delivery uses `password_reset_deliveries` as the durable outbox. New kinds contain encrypted material only at Identity; Notification receives only the delivery ID and fetches recipient/link during the existing protected lease. Confirmed-address notices contain no token or new address. SMTP failures retry through the existing bounded worker and do not undo the email change. SMTP ACK loss can duplicate a message, but cannot consume a token twice. No token is placed in an event/job payload, analytics or browser storage; links use URL fragments that the frontend removes on load.

The existing worker clears expired delivery ciphertext. An hourly sweep clears expired target ciphertext and purges old verification/change operations and their completed delivery records after the configured retention period. Expiry checks reject tokens even when workers are stopped. Audit logs and confirmed account data are not purged by this job.

Rollback: turn off new self-service entry points, retain schema 8 and all revocations/confirmed emails, and use a compatible binary. Existing queued notices still need a compatible delivery consumer. Do not run migration down: it intentionally refuses destructive reversal. Changing the existing encryption key with pending ciphertext requires the existing drain/invalidation procedure.

Watch Identity delivery backlog/retry/failure logs, 400 confirmation/replay rates, 429 verification/change requests, unexpected 409 profile conflicts, and `account_token_purge_failed`. Audit actions are `profile_updated`, `password_changed`, `email_change_requested`, `email_changed`, `email_verified`, and `self_sessions_revoked`; they include request correlation without raw email/phone/password/token data.

## Reproducible local verification

Start only the isolated test infrastructure:

```powershell
docker compose -f deploy/identity-test.compose.yml up -d --wait postgres redis
$env:GOCACHE='D:\shopee\.cache\go-build'
$env:IDENTITY_TEST_DATABASE_URL='postgres://identity_test@localhost:55431/identity_test?sslmode=disable'
$env:IDENTITY_TEST_REDIS_URL='redis://localhost:56391/0'
# From backend/
go test -count=1 ./services/identity/... ./services/notification/... ./pkg/middleware/...
go vet ./services/identity/... ./services/notification/... ./pkg/middleware/...
# From frontend/
npm test
npm run typecheck
$env:E2E_CHANNEL='msedge' # or chrome / installed Playwright Chromium
npx --no-install playwright test --config=playwright.account.config.ts
```

The browser configuration starts a real Identity test fixture on 3999 with isolated PostgreSQL schema and Redis; Next runs on 3100. It covers buyer/vendor/admin profiles and password changes, CAS errors, account cache isolation, individual/all session revocation and one-time email verification/change. A test-only authenticated mailbox exercises encrypted storage, lease and message construction; it replaces SMTP delivery, not Identity APIs. Those routes exist only in Go test code, never in the server binary. No shared application accounts are used. `ACCOUNT_E2E_REUSE_BUILD=true` skips rebuilding only when `.next` was already built with `NEXT_PUBLIC_API_BASE_URL=http://localhost:3999` and current source.

Local acceptance on 2026-10-13 is recorded in the AF-12 plan. It does not establish staging SMTP, gateway/HTTPS or production readiness. Stop the test Compose services after testing; do not touch the development stack or its volumes.
