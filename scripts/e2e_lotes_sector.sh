#!/usr/bin/env bash
# ============================================================================
# E2E — contrato lotes/sector (docs/backend-go-lotes-sector.md)
# Simula dos Raspberrys (ENTRADA_HORNO + SALIDA_HORNO) del mismo sector contra
# el binario real, con un PostgreSQL descartable. Cubre el flujo feliz, la
# idempotencia, los casos borde y el SSE.
#
# Uso:  bash scripts/e2e_lotes_sector.sh
# Env:  E2E_BASE_DB_URL (default: smart-check-db local :5433)
#       E2E_PORT (default 18080)
# ============================================================================
set -uo pipefail

BASE_DB="${E2E_BASE_DB_URL:-postgres://smartcheck:smartcheck123@localhost:5433/smart_check?sslmode=disable}"
PORT="${E2E_PORT:-18080}"
JWT_SECRET="e2e-jwt-secret-0123456789-0123456789-xyz"
GOOGLE_CLIENT_ID="e2e.apps.googleusercontent.com"
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"
TMP="$(mktemp -d)"
BIN="$TMP/sca-server"
DB_NAME="sca_e2e_$(date +%s)"
DB_URL="$(echo "$BASE_DB" | sed -E "s#(://[^/]+/)[^?]+#\1${DB_NAME}#")"
BASE_URL="http://127.0.0.1:${PORT}"
SERVER_PID=""
FAILED=0
PASSED=0

ENTRADA_SECRET="e2e-entrada-$(openssl rand -hex 12)"
SALIDA_SECRET="e2e-salida-$(openssl rand -hex 12)"
DEG_SALIDA_SECRET="e2e-deg-$(openssl rand -hex 12)"
# Credencial OAuth del E2E: la del seed de schema.sql (admin@fermar.com.ar).
E2E_PASSWORD="password123"

DEV_ENTRADA="11111111-1111-1111-1111-111111111111"
DEV_SALIDA="22222222-2222-2222-2222-222222222222"
DEV_DEG="33333333-3333-3333-3333-333333333333"
PRODUCTO="a1b2c3d4-5678-90ab-cdef-1234567890ab"
PRODUCTO_INEXISTENTE="99999999-9999-9999-9999-999999999999"

uuid() { cat /proc/sys/kernel/random/uuid; }
sha256() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }

ok()   { PASSED=$((PASSED+1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad()  { FAILED=$((FAILED+1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
check(){ if [ "$1" = "$2" ]; then ok "$3 (=$1)"; else bad "$3 (got '$1', want '$2')"; fi; }

HTTP_CODE=""; BODY=""
call() { # call METHOD PATH [curl args...]
  local method="$1" path="$2"; shift 2
  HTTP_CODE="$(curl -sS -o "$TMP/body" -w '%{http_code}' -X "$method" "$BASE_URL$path" "$@")"
  BODY="$(cat "$TMP/body")"
}
jqv() { printf '%s' "$BODY" | jq -r "$1"; }
dev() { printf 'Authorization: Bearer %s' "$1"; }
json() { printf 'Content-Type: application/json'; }

cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
  sleep 0.3
  psql "$BASE_DB" -c "DROP DATABASE IF EXISTS \"$DB_NAME\" WITH (FORCE)" >/dev/null 2>&1
  rm -rf "$TMP"
}
trap cleanup EXIT

echo "== Provisionando DB $DB_NAME =="
psql "$BASE_DB" -c "CREATE DATABASE \"$DB_NAME\"" >/dev/null || { echo "no se pudo crear la DB"; exit 1; }
psql "$DB_URL" -f "$ROOT_DIR/database/schema.sql" >/dev/null 2>&1 || { echo "no se pudo aplicar el schema"; exit 1; }

echo "== Seed (sector + 3 dispositivos + secretos) =="
psql "$DB_URL" >/dev/null <<SQL
INSERT INTO sectores (id, nombre) VALUES ('horno-e2e','Horno E2E') ON CONFLICT DO NOTHING;
INSERT INTO sectores (id, nombre) VALUES ('horno-deg','Horno Degradado') ON CONFLICT DO NOTHING;
INSERT INTO dispositivos (id, nombre, tipo, sector_id, auth_status, secret_hash) VALUES
  ('$DEV_ENTRADA','pi-entrada','ENTRADA_HORNO','horno-e2e','active','$(sha256 "$ENTRADA_SECRET")'),
  ('$DEV_SALIDA','pi-salida','SALIDA_HORNO','horno-e2e','active','$(sha256 "$SALIDA_SECRET")'),
  ('$DEV_DEG','pi-deg-salida','SALIDA_HORNO','horno-deg','active','$(sha256 "$DEG_SALIDA_SECRET")')
ON CONFLICT (id) DO NOTHING;
SQL

echo "== Build + boot (LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS=60) =="
go build -o "$BIN" ./cmd/server || { echo "build falló"; exit 1; }
DATABASE_URL="$DB_URL" JWT_SECRET="$JWT_SECRET" GOOGLE_CLIENT_ID="$GOOGLE_CLIENT_ID" \
  PORT="$PORT" LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS=60 "$BIN" >"$TMP/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 50); do
  curl -sf "$BASE_URL/health" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "$BASE_URL/health" >/dev/null || { echo "server no levantó"; cat "$TMP/server.log"; exit 1; }

echo
echo "== Fase A: flujo feliz =="

call GET /api/v1/productos -H "$(dev "$ENTRADA_SECRET")"
check "$HTTP_CODE" 200 "GET /productos (device) → 200"
check "$(jqv '.success')" true "productos.success"
check "$(jqv '.data | length > 0')" true "productos.data no vacío"

call GET /api/v1/dispositivos/sector -H "$(dev "$ENTRADA_SECRET")"
check "$HTTP_CODE" 200 "GET /dispositivos/sector (device) → 200"
check "$(jqv '.data.sector_id')" "horno-e2e" "sector_id"
check "$(jqv '.data.tipo')" "ENTRADA_HORNO" "tipo del device"
check "$(jqv '.data.companeros[0].type')" "SALIDA_HORNO" "companero SALIDA_HORNO"

KEY1="$(uuid)"
call POST /api/v1/lotes/inicio -H "$(dev "$ENTRADA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$KEY1\",\"producto_id\":\"$PRODUCTO\"}"
check "$HTTP_CODE" 201 "POST /lotes/inicio (entrada) → 201"
check "$(jqv '.data.creado')" true "inicio.creado=true"
LOTE="$(jqv '.data.lote.id')"
check "$(jqv '.data.lote.estado')" "ABIERTO" "lote.estado=ABIERTO"
check "$(jqv '.data.lote.abierto_por.type')" "ENTRADA_HORNO" "abierto_por.type"

call POST /api/v1/lotes/inicio -H "$(dev "$ENTRADA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$KEY1\",\"producto_id\":\"$PRODUCTO\"}"
check "$HTTP_CODE" 200 "inicio reintento misma key → 200"
check "$(jqv '.data.creado')" false "inicio reintento creado=false"
check "$(jqv '.data.lote.id')" "$LOTE" "mismo lote"

call POST /api/v1/lotes/inicio -H "$(dev "$ENTRADA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$(uuid)\",\"producto_id\":\"$PRODUCTO\"}"
check "$(jqv '.data.creado')" false "inicio otra key → attach (get-or-create)"

echo "== Fase B: eventos (salida) + idempotencia =="
E1="$(uuid)"; E2="$(uuid)"
BATCH="{\"eventos\":[
  {\"evento_id\":\"$E1\",\"producto_id\":\"$PRODUCTO\",\"estado\":\"ok\",\"confianza\":0.95,\"pista\":7,\"frame\":1234,\"modelo_id\":\"tostadas-v2\"},
  {\"evento_id\":\"$E2\",\"producto_id\":\"$PRODUCTO\",\"estado\":\"quemado\",\"confianza\":0.91}
]}"
call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$BATCH"
check "$HTTP_CODE" 200 "POST /lotes/{id}/eventos → 200"
check "$(jqv '.data.aceptados')" 2 "aceptados=2"
check "$(jqv '.data.duplicados')" 0 "duplicados=0"
check "$(jqv '.data.lote.conteos.ok')" 1 "conteos.ok=1"
check "$(jqv '.data.lote.conteos.quemado')" 1 "conteos.quemado=1"
check "$(jqv '.data.lote.conteos.crudo')" null "conteos.crudo=null"
check "$(jqv '.data.lote.conteos.total')" 2 "conteos.total=2"

call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$BATCH"
check "$HTTP_CODE" 200 "reintento del mismo batch → 200"
check "$(jqv '.data.aceptados')" 0 "reintento aceptados=0"
check "$(jqv '.data.duplicados')" 2 "reintento duplicados=2"
check "$(jqv '.data.lote.conteos.total')" 2 "reintento no duplica conteos"

echo "== Fase C: casos borde =="
call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" \
  -d "{\"eventos\":[{\"evento_id\":\"$(uuid)\",\"producto_id\":\"$PRODUCTO_INEXISTENTE\",\"estado\":\"ok\"}]}"
check "$HTTP_CODE" 422 "producto inconsistente → 422"
check "$(jqv '.errors.code')" "producto_inconsistente" "code producto_inconsistente"

call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" \
  -d "{\"eventos\":[{\"evento_id\":\"$(uuid)\",\"producto_id\":\"$PRODUCTO\",\"estado\":\"podrido\"}]}"
check "$HTTP_CODE" 400 "estado inválido → 400"
check "$(jqv '.errors.code')" "payload_invalido" "code payload_invalido"

call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d '{"eventos":[]}'
check "$HTTP_CODE" 400 "batch vacío → 400"

EVS=""
for i in $(seq 0 100); do EVS="$EVS{\"evento_id\":\"$(printf '00000000-0000-0000-0000-%012x' "$i")\",\"producto_id\":\"$PRODUCTO\",\"estado\":\"ok\"},"; done
call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "{\"eventos\":[${EVS%,}]}"
check "$HTTP_CODE" 413 "batch >100 → 413"

call POST "/api/v1/lotes/no-es-uuid/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$BATCH"
check "$HTTP_CODE" 404 "lote_id no UUID → 404"

call POST /api/v1/lotes/inicio -H "$(dev "$ENTRADA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$(uuid)\",\"producto_id\":\"$PRODUCTO_INEXISTENTE\"}"
check "$HTTP_CODE" 422 "producto desconocido → 422"
check "$(jqv '.errors.code')" "producto_desconocido" "code producto_desconocido"

call GET /api/v1/lotes/abierto -H "$(dev "$SALIDA_SECRET")"
check "$HTTP_CODE" 200 "GET /lotes/abierto (device) → 200"
check "$(jqv '.data.lote.id')" "$LOTE" "abierto.id coincide"

echo "== Fase D: cierre + gate de inactividad + idempotencia =="
CIERRE="{\"idempotency_key\":\"$(uuid)\",\"motivo\":\"sin_detecciones\",\"conteos\":{\"ok\":1,\"crudo\":null,\"quemado\":1,\"total\":2}}"
call POST "/api/v1/lotes/$LOTE/cierre" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$CIERRE"
check "$HTTP_CODE" 409 "cierre con sector activo → 409"
check "$(jqv '.errors.code')" "sector_activo" "code sector_activo"

psql "$DB_URL" -c "UPDATE lotes_productivos SET ultimo_evento_en = now() - interval '120 seconds' WHERE id='$LOTE'" >/dev/null
call POST "/api/v1/lotes/$LOTE/cierre" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$CIERRE"
check "$HTTP_CODE" 200 "cierre tras inactividad → 200"
check "$(jqv '.data.cerrado')" true "cerrado=true"
check "$(jqv '.data.lote.estado')" "CERRADO" "estado=CERRADO"
check "$(jqv '.data.lote.conteos.total')" 2 "conteos finales autoritativos"

call POST "/api/v1/lotes/$LOTE/cierre" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$CIERRE"
check "$HTTP_CODE" 200 "reintento de cierre → 200 (nunca 409)"
check "$(jqv '.data.lote.estado')" "CERRADO" "reintento sigue CERRADO"

call POST "/api/v1/lotes/$LOTE/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" -d "$BATCH"
check "$HTTP_CODE" 409 "eventos sobre lote cerrado → 409"
check "$(jqv '.errors.code')" "lote_cerrado" "code lote_cerrado"

echo "== Fase E: historial (device + OAuth) =="
call GET /api/v1/lotes -H "$(dev "$ENTRADA_SECRET")"
check "$HTTP_CODE" 200 "GET /lotes (device) → 200"
check "$(jqv '.total')" 1 "historial total=1"
check "$(jqv '.data[0].id')" "$LOTE" "historial primer lote"
check "$(jqv '.data[0].conteos.total')" 2 "historial conteos"

call GET "/api/v1/lotes?sector_id=horno-e2e"
check "$HTTP_CODE" 401 "GET /lotes sin credencial → 401"

curl -sS -c "$TMP/cookies" -o /dev/null -X POST "$BASE_URL/api/v1/auth/login" \
  -H "$(json)" -d "{\"email\":\"admin@fermar.com.ar\",\"password\":\"$E2E_PASSWORD\"}"
COOKIE="$(awk '/session_token/{print $6"="$7}' "$TMP/cookies" | tail -1)"
if [ -z "$COOKIE" ]; then bad "login OAuth no entregó cookie"; else ok "login OAuth → cookie"; fi

call GET "/api/v1/lotes?sector_id=horno-e2e" -H "Cookie: $COOKIE"
check "$HTTP_CODE" 200 "GET /lotes (OAuth + sector_id) → 200"
check "$(jqv '.data[0].id')" "$LOTE" "historial OAuth"

call GET /api/v1/lotes -H "Cookie: $COOKIE"
check "$HTTP_CODE" 400 "GET /lotes OAuth sin sector_id → 400"

call GET /api/v1/productos -H "Cookie: $COOKIE"
check "$HTTP_CODE" 200 "GET /productos (OAuth) → 200"

call GET /api/v1/productos -H "Authorization: Bearer token-invalido"
check "$HTTP_CODE" 401 "GET /productos token inválido → 401"

echo "== Fase F: sector incompleto (degradado) =="
call GET /api/v1/dispositivos/sector -H "$(dev "$DEG_SALIDA_SECRET")"
check "$HTTP_CODE" 200 "sector degradado → 200"
check "$(jqv '.data.companeros | length')" 0 "sin compañeros"

call POST /api/v1/lotes/inicio -H "$(dev "$DEG_SALIDA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$(uuid)\",\"producto_id\":\"$PRODUCTO\"}"
check "$HTTP_CODE" 201 "salida abre lote degradado → 201"
check "$(jqv '.data.lote.abierto_por.type')" "SALIDA_HORNO" "abierto_por.type=SALIDA_HORNO"

echo "== Fase G: SSE (OAuth) =="
curl -sS -N -m 6 -H "Cookie: $COOKIE" "$BASE_URL/api/v1/lotes/events" >"$TMP/sse.txt" 2>/dev/null &
SSE_PID=$!
sleep 0.8
KEY2="$(uuid)"
call POST /api/v1/lotes/inicio -H "$(dev "$ENTRADA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$KEY2\",\"producto_id\":\"$PRODUCTO\"}"
LOTE2="$(jqv '.data.lote.id')"
check "$HTTP_CODE" 201 "SSE: segundo lote abierto → 201"
call POST "/api/v1/lotes/$LOTE2/eventos" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" \
  -d "{\"eventos\":[{\"evento_id\":\"$(uuid)\",\"producto_id\":\"$PRODUCTO\",\"estado\":\"ok\"}]}"
check "$HTTP_CODE" 200 "SSE: eventos sobre lote 2 → 200"
psql "$DB_URL" -c "UPDATE lotes_productivos SET ultimo_evento_en = now() - interval '120 seconds' WHERE id='$LOTE2'" >/dev/null
call POST "/api/v1/lotes/$LOTE2/cierre" -H "$(dev "$SALIDA_SECRET")" -H "$(json)" \
  -d "{\"idempotency_key\":\"$(uuid)\",\"motivo\":\"manual\",\"conteos\":{\"ok\":1,\"crudo\":null,\"quemado\":null,\"total\":1}}"
check "$HTTP_CODE" 200 "SSE: cierre lote 2 → 200"
wait "$SSE_PID" 2>/dev/null
for ev in lote.creado lote.actualizado lote.cerrado; do
  if grep -q "event: $ev" "$TMP/sse.txt"; then ok "SSE recibió $ev"; else bad "SSE no recibió $ev"; fi
done

echo
echo "============================================"
echo "E2E: $PASSED OK, $FAILED FAIL"
echo "============================================"
[ "$FAILED" -eq 0 ] || { echo "--- server.log (tail) ---"; tail -20 "$TMP/server.log"; exit 1; }
exit 0
