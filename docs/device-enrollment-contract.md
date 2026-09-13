# Device enrollment contract

Accepted implementation contract (2026-09-12). Operator writes require Supervisor or Administrador; reads allow authenticated users. No shared API-key fallback. No QR/USB or mTLS required. Never deploy without verified HTTPS, distinct deployment audience, proper frontend origins and safe user accounts.

## Identity and lifecycle

Device `authStatus` is separate from heartbeat `estado`:
- `unenrolled`: legacy catalog entry without credential.
- `active`: current non-revoked credential may authenticate.
- `disabled`: retained credential blocked; enable restores active.
- `revoked`: old credential permanently invalid. Reprovision can activate a **new** key for the same UUID/history.

First enrollment creates UUID only in successful redemption transaction. Reprovision immediately revokes old credential, cancels pending invitations and issues a replacement code. Revoke cancels pending replacement invitations. Enable only active/disabled; disable only active/disabled. Same-state enable/disable/revoke idempotent, other invalid transitions 409. Credential fingerprints are globally unique forever; never reuse revoked keys. No hard delete or direct catalog create API.

Lifecycle/admission serialize on the same DB device row: after lifecycle commit subsequent admission is blocked, already-admitted operations may finish. This does not cancel already dispatched oven commands. Oven-specific authorization and business exactly-once delivery are outside this change.

## Signature wire format

Go existing golang-jwt/jwt/v5 + crypto/ed25519; Python cryptography + PyJWT. Private key PKCS#8 PEM remains on Pi. Public JWK has exactly `{"kty":"OKP","crv":"Ed25519","x":"<unpadded base64url 32-byte public key>"}`. Reject unknown/private JWK fields or noncanonical encodings. Fingerprint is RFC7638 SHA256 of exact UTF-8 `{"crv":"Ed25519","kty":"OKP","x":"..."}`, base64url unpadded.

Header `Authorization: DeviceProof <compact JWS>` and exact `Content-Type: application/json`. Protected header exactly `alg=EdDSA`, `typ=sca-device+jwt` (operational) or `sca-enrollment+jwt` (provision/recover), `kid=<fingerprint>`. Reject other algorithms/headers, duplicate JSON members, multiple Authorization values. Do not fetch keys from headers.

Required claims, exact types: `sub` device UUID (operational) or `urn:sca:enrollment-key:<fingerprint>` (enrollment), `aud` single configured string, integer Unix `iat`, `exp`, `jti` unpadded base64url 16 random bytes, `htm` exact method, `rt` exact origin-form raw RequestURI including escapes/query, `bhash` unpadded base64url SHA256 of raw transmitted body. Client exp=iat+60. DB-time validation: 0 < exp-iat <=60, iat<=now+30, iat>=now-90, now<exp. Audience configured as DEVICE_AUTH_AUDIENCE on both ends, e.g. https://api.example.com/api/v1. Never derive from request Host.

Signed POST endpoints accept no queries, path aliases/trailing slashes, Content-Encoding or redirects. Validate target before ServeMux redirects. Python prepares bytes once, signs final prepared target/body, sends the same bytes with allow_redirects=False. No JSON reserialization after signing.

## HTTP API

Existing envelope `{success,message,data}`; stable errors `{success:false,message:<Spanish>,errors:{code:<machine code>}}`. All new responses Cache-Control:no-store. Dates UTC RFC3339. Reject unknown DTO fields, duplicate JSON fields, extra trailing documents.

Human management (JWT cookie, Supervisor/Admin mutations):
- POST `/api/v1/dispositivos/enrollments`, body `{nombre,ubicacion?,whepUrl?}`. Existing trimmed validation. 201 data `{enrollmentId,dispositivoId:null,status:"pending",code,expiresAt}`. No device row/UUID yet.
- GET same path, data array of currently pending/unexpired invitations `{enrollmentId,dispositivoId:null-or-existing,nombre,ubicacion,whepUrl,status:"pending",createdAt,expiresAt}`. Never expose code/hash.
- POST `/api/v1/dispositivos/enrollments/{enrollmentId}/cancel`, body `{}`. 200 `{enrollmentId,status:"cancelled"}`. Cancel idempotent; consumed =>409 enrollment_consumed. Replacement cancellation does not restore old key.
- POST `/api/v1/dispositivos/{id}/disable|enable|revoke`, body `{}`. 200 device read object.
- POST `/api/v1/dispositivos/{id}/reprovision`, body `{}`. 201 invitation with existing dispositivoId. Concurrent issuance serialized; last committed wins and predecessors cancelled. No automatic management issuance retry on uncertain response.
- GET `/api/v1/dispositivos` remains. PUT metadata remains Supervisor/Admin only with `{dispositivoId,nombre,ubicacion,whepUrl}` and no key/lifecycle fields. POST/DELETE direct catalog return 405. History/SSE remain human JWT reads.

Public proof-authenticated enrollment **uses user's required endpoint**:
- POST `/api/v1/dispositivos/provision`, body `{code,publicKey:<JWK>}`, enrollment proof signed with supplied key. **No deviceId in request.** 201 data `{enrollmentId,dispositivoId,keyFingerprint,authStatus:"active",enrolledAt,audience}`. This identity descriptor plus locally held private key is the credential; no bearer token or server-issued private key/certificate.
- POST `/api/v1/dispositivos/enrollments/recover`, body `{publicKey:<JWK>}`, fresh enrollment proof, no code/UUID required. Lookup completed mapping by globally unique fingerprint. 200 same data (active or disabled); 404 enrollment_not_found if absent; 409 credential_revoked for revoked/replaced; 401 invalid_device_proof for bad proof. Never reactivate on recovery.
- Unknown/expired/cancelled/consumed codes uniformly 400 enrollment_unavailable. Consumed code never succeeds again; recovery is a separate proof-only action.

Code: 32 CSPRNG bytes (43-character case-sensitive unpadded base64url), TTL fixed 15 minutes. Store SHA256(code ASCII) only; plaintext only once in issuance response. Enrollment IDs 16 random bytes base64url, not device UUIDs. No codes in URLs, proof claims, logs, analytics, browser persistence or backups.

## Database and consistency

Use existing embedded idempotent schema, transaction advisory lock immediately after BEGIN. Preserve all existing devices/history, default legacy auth_status=unenrolled; remove recurring sample-device insertion. No startup reset of credentials/state.

Tables/fields:
- dispositivos: auth_status checked enum/default unenrolled, current_key_fingerprint nullable, auth_updated_at.
- device_credentials: fingerprint PK, dispositivo_id FK deletion restricted, 32-byte public_key, enrollment_id unique, enrolled_at, revoked_at nullable; partial unique one nonrevoked credential/device; retain historical rows.
- device_enrollments: opaque ID PK, code_hash unique nullable/32 bytes, target_dispositivo_id nullable, requested metadata, created_by/timestamps/expiry, consumed_at/cancelled_at mutually exclusive, result dispositivo_id/key_fingerprint together only when consumed. One pending replacement/device (cancel expired predecessors).
- device_request_replays: (key_fingerprint,jti) PK with expires_at + expiry index, no credential FK because initial enrollment precedes credential creation.
- lifecycle audit: actor/action/device/enrollment/old-new statuses/time, no secrets.
- nullable dispositivo_id provenance FKs on lotes_productivos and historial_consignas; old rows remain null. New node-originated writes derive provenance from typed authenticated principal. No oven ownership model invented.

Replay: after bounded parse/crypto/binding, transaction locks device row for operational proof, reloads active/current/nonrevoked credential, checks DB time, inserts unique (kid,jti), commits before business invocation. Same replay table for verified enrollment proofs. Duplicate =>401 proof_replayed. DB failure =>503 auth_store_unavailable, fail closed. Retain through exp+30 seconds; cleanup DB time. Replay protection durable across process/restarts, not business idempotency.

Redemption transaction locks existing target device first then invitation; for new enrollment locks invitation only. Revalidate pending/current/unexpired under locks; reject ever-used key; new UUID row or existing target; insert credential; activate current key; record consumed result; clear code_hash; commit atomically. Rollback leaves no partial identity/consumption. Cancel/revoke/reprovision obey same lock order.

## Existing node routes and reads

Operational proof required on POST `/api/v1/dispositivos/ping`, `/api/v1/lotes`, `/api/v1/lotes/inicio`. No new /telemetry endpoint. Shared key alone/human JWT alone rejected. Remove old handler checks and Python shared-key injection. Typed principal required; handlers/services fail closed without it.

Ping keeps payload and requires dispositivoId==principal ID else403 device_identity_mismatch; storage uses principal. Lote/inicio preserve business payload/success responses, reject supplied identity overrides, provenance comes internally from principal. Do not automatically retry ambiguous lote/inicio outcomes: may duplicate batch/physical command.

Device read keeps existing telemetry fields and always adds `{authStatus,keyFingerprint:null-or-string,enrolledAt:null-or-time,authUpdatedAt,pendingEnrollment:null-or-{enrollmentId,expiresAt}}`. Current nonrevoked key shown for active/disabled only. Catalog/security read from DB merged with memory telemetry, never cache-authoritative auth. Pending first invitations displayed separately without fabricated UUIDs. Refetch reads after actions/reconnect; lifecycle/connectivity labels distinct.

## Raspberry local lifecycle

Independent CLI `python -m device_enrollment enroll|recover`, optional `--env-file /absolute/path/device.env`, `--api-base-url`, `--audience`, `--identity-dir`. Never plaintext --code argument. Env DEVICE_API_BASE_URL (ending /api/v1), DEVICE_AUTH_AUDIENCE, DEVICE_ENROLLMENT_CODE, DEVICE_IDENTITY_DIR (default /var/lib/smart-check/device). CLI only reads explicitly selected env file, no AI/GUI/hardware imports. Code via env/file or no-echo prompt.

Directory0700, files0600 service-owned, reject symlinks/unsafe ownership/permissions; exclusive local lock. private-key.pem PKCS8; identity.json version/phase pending|enrolled/API URL/audience/fingerprint/nullable device UUID/enrollment ID/enrolled timestamp. Persist key then pending metadata before any request. Reuse orphaned/pending key after crash; corrupt/missing enrolled key is hard failure. Secure same-directory tempfile, fsync file, atomic replace, fsync parent.

Pending retry: fresh proof recover first; 200 persist enrolled identity then clean code;404 redeem same key/code; ambiguous failure retry via recover; enrollment_unavailable recover again then require new invite; revoked hard failure needs deliberate reprovision/new key. Never rotate merely on lost response. Provide explicit deliberate local reset/reprovision mechanism safely so existing revoked identity can consume replacement invitation with fresh key, documented and tested.

After identity durable, remove DEVICE_ENROLLMENT_CODE from process config and explicit env-file assignments only, preserving other content without secret backups. Cleanup failure nonsecret warning/nonzero but keep successful identity. Cannot erase parent shell/container env; instruct source cleanup. No guarantee SSD/backup secure erasure.

App startup and all three senders load persistent identity and signed transport; env code startup support or CLI documented; no API-key fallback. Allowlisted logging only path without query, method/status/duration/safe error/device ID/fingerprint. Never headers/proofs/private keys/code/payload/response objects or secret-rich exception strings.

## Panel, limits, deployment

Operario read-only; Supervisor/Admin issue/cancel/edit/disable/enable/revoke/reprovision. Confirmation for revoke/reprovision explains immediate old-key invalidation; prevent duplicate submissions. Code only issuance modal, copy, expiry, clear on close/navigation/logout/expiry, no storage/reveal again. Separate pending invitations and lifecycle vs connectivity labels. Spanish copy, preserve visual design.

8KiB new JSON bodies, 1MiB operational, 4KiB proof, 2KiB raw target. Public proof endpoints IP 30/min burst10 before crypto; valid device120/min burst30; management mutations/user30/min burst10. Bounded per-replica limiter, Retry-After on429; untrusted forwarded headers ignored. Bounded server headers/timeouts; SSE remains functional.

TLS verified mandatory; explicit development opt-in only literal loopback HTTP. Backend behind private TLS proxy can accept internal HTTP but must not be publicly bypassable. Reject supplied browser Origin outside frontend allowlist on management writes; cookie-forwarded server actions may omit Origin. Pin existing human JWT to HS256, separate device verifier. Production user seeds remain a deployment concern; do not expose unsafe defaults.

## Evidence gates

Phase A: isolated real PostgreSQL fresh+legacy upgrade+double schema; concurrent redeem one winner/rollback; lost-response recovery after restart; durable replay concurrent/restart; tamper/alg/time/audience/type/proof/target/body rejection; auth store fail closed; all legacy endpoints deny old auth; lifecycle race/new key/UUID history retention; RBAC including old routes; provenance; redaction. TEST_DATABASE_URL-skipped tests do not establish these.

Phase B: actual Python->Go provision/recovery/ping/lote/inicio and negative tamper/replay/revocation; temporary filesystem crash/restart/permissions/cleanup; prepared Unicode body/no redirects; lightweight CLI imports; frontend role/state/modal/action evidence. Preserve preexisting Raspberry changes. Oracle reviews each phase, parent reconciles. No commits/deployments authorized.
