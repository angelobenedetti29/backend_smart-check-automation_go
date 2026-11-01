#!/usr/bin/env bash
# ============================================================================
# E2E — flujo de control del horno túnel contra backend real + PostgreSQL real.
#
# Levanta un PostgreSQL 16 descartable (puerto 15532), el backend real
# (18080) y un sidecar predictor falso (18090). Siembra un sector 'e2e' con
# un producto de receta completa y dos nodos Raspberry (ENTRADA_HORNO /
# SALIDA_HORNO), y corre el driver Python que simula el contrato HTTP de la Pi:
#
#   apertura AUTOMATICO -> telemetría + detecciones -> propuesta MPC ->
#   ack de consigna -> espejo de setpoints en el lote -> cierre.
#
# Al final audita con psql: decisiones_control, aplicado_por=MPC,
# confianza_promedio ≈ 0.9 y ausencia de alerta PRECALENTAMIENTO_TIMEOUT.
#
# Uso:  ./scripts/e2e_horno_tunel.sh
# No toca la base de desarrollo (puertos/container propios).
# ============================================================================
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

PG_CONTAINER="e2e-horno-pg"
PG_PORT=15532
BACKEND_PORT=18080
PREDICTOR_PORT=18090
BASE_URL="http://127.0.0.1:${BACKEND_PORT}"
DATABASE_URL="postgres://e2e:e2e@127.0.0.1:${PG_PORT}/e2e?sslmode=disable"

PRODUCTO_ID="11111111-1111-4111-8111-111111111111"
ENTRADA_ID="22222222-2222-4222-8222-222222222222"
SALIDA_ID="33333333-3333-4333-8333-333333333333"
ENTRADA_SECRET="e2e-entrada"
SALIDA_SECRET="e2e-salida"

JWT_SECRET="e2e-jwt-secret-0123456789-0123456789"
GOOGLE_CLIENT_ID="e2e.apps.googleusercontent.com"

TMP="$(mktemp -d)"
BIN="$TMP/sca-server"
BACKEND_PID=""
PREDICTOR_PID=""
PASSED=0
FAILED=0

ok()  { PASSED=$((PASSED + 1)); printf '  \033[32mOK\033[0m   %s\n' "$1"; }
bad() { FAILED=$((FAILED + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
check_eq() { if [ "$1" = "$2" ]; then ok "$3 (=$1)"; else bad "$3 (got '$1', want '$2')"; fi; }

psql_q() {
  docker exec -i "$PG_CONTAINER" psql -U e2e -d e2e -tA -v ON_ERROR_STOP=1 -c "$1"
}

cleanup() {
  [ -n "$BACKEND_PID" ] && kill "$BACKEND_PID" 2>/dev/null || true
  [ -n "$PREDICTOR_PID" ] && kill "$PREDICTOR_PID" 2>/dev/null || true
  docker rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT

show_logs() {
  echo "== backend.log (tail) =="
  tail -n 60 "$TMP/backend.log" 2>/dev/null || true
  echo "== predictor.log (tail) =="
  tail -n 20 "$TMP/predictor.log" 2>/dev/null || true
}

echo "== Requisitos =="
command -v docker >/dev/null 2>&1 || { echo "FAIL: falta docker"; exit 1; }
command -v go >/dev/null 2>&1 || { echo "FAIL: falta go"; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "FAIL: falta python3"; exit 1; }
echo "  OK   docker/go/python3 presentes"

echo "== PostgreSQL descartable en :${PG_PORT} (${PG_CONTAINER}) =="
docker rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$PG_CONTAINER" \
  -e POSTGRES_USER=e2e -e POSTGRES_PASSWORD=e2e -e POSTGRES_DB=e2e \
  -p "127.0.0.1:${PG_PORT}:5432" postgres:16 >/dev/null

ready=0
for _ in $(seq 1 80); do
  # pg_isready responde también durante el initdb, cuando la DB aún no existe:
  # esperamos a que la base 'e2e' acepte una consulta real.
  if docker exec "$PG_CONTAINER" psql -U e2e -d e2e -tAc 'SELECT 1' >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [ "$ready" -ne 1 ]; then
  echo "FAIL: PostgreSQL no levantó"
  docker logs "$PG_CONTAINER" 2>&1 | tail -n 30 || true
  exit 1
fi
echo "  OK   PostgreSQL listo"

docker exec -e PGOPTIONS="-c client_min_messages=warning" -i "$PG_CONTAINER" \
  psql -v ON_ERROR_STOP=1 -U e2e -d e2e \
  < "$ROOT_DIR/database/schema.sql" >/dev/null
echo "  OK   schema.sql aplicado"

HASH_ENTRADA="$(printf '%s' "$ENTRADA_SECRET" | sha256sum | cut -d' ' -f1)"
HASH_SALIDA="$(printf '%s' "$SALIDA_SECRET" | sha256sum | cut -d' ' -f1)"

docker exec -i "$PG_CONTAINER" psql -v ON_ERROR_STOP=1 -U e2e -d e2e >/dev/null <<SQL
INSERT INTO sectores (id, nombre) VALUES ('e2e', 'E2E') ON CONFLICT (id) DO NOTHING;

INSERT INTO productos (
  id, nombre,
  peso_referencia_kg, tolerancia_peso_pct,
  dimension_base_cm, tolerancia_dimension_cm,
  temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max,
  temp_setpoint, velocidad_cinta_setpoint,
  delta_temp1_max_c, delta_vel_max
) VALUES (
  '${PRODUCTO_ID}', 'E2E Horno Túnel',
  0.250, 5.00,
  12.00, 0.50,
  180.00, 220.00, 0.10, 0.60,
  200.00, 0.40,
  10.00, 0.050
) ON CONFLICT (id) DO NOTHING;

INSERT INTO dispositivos (id, nombre, tipo, sector_id, auth_status, secret_hash) VALUES
  ('${ENTRADA_ID}', 'e2e-pi-entrada', 'ENTRADA_HORNO', 'e2e', 'active', '${HASH_ENTRADA}'),
  ('${SALIDA_ID}',  'e2e-pi-salida',  'SALIDA_HORNO',  'e2e', 'active', '${HASH_SALIDA}')
ON CONFLICT (id) DO NOTHING;
SQL
echo "  OK   seed: sector e2e + producto + 2 dispositivos"

echo "== Predictor stub en :${PREDICTOR_PORT} =="
python3 "$ROOT_DIR/scripts/e2e_predictor_stub.py" --port "$PREDICTOR_PORT" \
  >"$TMP/predictor.log" 2>&1 &
PREDICTOR_PID=$!

echo "== Build + backend en :${BACKEND_PORT} =="
go build -o "$BIN" ./cmd/server
DATABASE_URL="$DATABASE_URL" \
  PORT="$BACKEND_PORT" \
  JWT_SECRET="$JWT_SECRET" \
  GOOGLE_CLIENT_ID="$GOOGLE_CLIENT_ID" \
  CONTROL_ACTUADOR=pi \
  MPC_PERMITIR_PLACEHOLDER=true \
  PREDICTOR_URL="http://127.0.0.1:${PREDICTOR_PORT}" \
  MPC_CONFIANZA_MIN=0.5 \
  MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS=120 \
  MPC_PREHEAT_POLL_SEGUNDOS=1 \
  "$BIN" >"$TMP/backend.log" 2>&1 &
BACKEND_PID=$!

healthy=0
for _ in $(seq 1 100); do
  if curl -sf "$BASE_URL/health" >/dev/null 2>&1; then
    healthy=1
    break
  fi
  sleep 0.2
done
if [ "$healthy" -ne 1 ]; then
  echo "FAIL: el backend no respondió /health"
  show_logs
  exit 1
fi
echo "  OK   backend health"

echo
echo "== Driver E2E: flujo de control =="
if ! python3 "$ROOT_DIR/scripts/e2e_horno_tunel.py" \
  --base-url "$BASE_URL" \
  --entrada-secret "$ENTRADA_SECRET" \
  --salida-secret "$SALIDA_SECRET" \
  --producto-id "$PRODUCTO_ID"; then
  echo
  echo "FAIL: driver E2E"
  show_logs
  exit 1
fi

echo
echo "== Auditoría en PostgreSQL =="
LOTE="$(psql_q "SELECT id FROM lotes_productivos WHERE sector_id='e2e' ORDER BY created_at DESC LIMIT 1")"
if [ -z "$LOTE" ]; then
  bad "no hay lote del sector e2e"
  show_logs
  exit 1
fi

DEC_COUNT="$(psql_q "SELECT COUNT(*) FROM decisiones_control WHERE lote_id='${LOTE}'")"
if [ "${DEC_COUNT:-0}" -gt 0 ]; then
  ok "decisiones_control tiene ${DEC_COUNT} fila(s) para el lote"
else
  bad "decisiones_control no tiene filas para el lote"
fi

LAST_ACK="$(psql_q "SELECT aplicado_por || '|' || COALESCE(aprobada_por, '') FROM decisiones_control WHERE lote_id='${LOTE}' AND temp_aplicado IS NOT NULL ORDER BY created_at DESC LIMIT 1")"
check_eq "$LAST_ACK" "MPC|" "última decisión ack: aplicado_por=MPC, aprobada_por=NULL"

ESTADO="$(psql_q "SELECT estado FROM lotes_productivos WHERE id='${LOTE}'")"
check_eq "$ESTADO" "CERRADO" "lote CERRADO"

CONF_OK="$(psql_q "SELECT (confianza_promedio IS NOT NULL AND abs(confianza_promedio - 0.9) < 0.01)::text FROM lotes_productivos WHERE id='${LOTE}'")"
check_eq "$CONF_OK" "true" "confianza_promedio != NULL y ≈ 0.9"

ALERTAS="$(psql_q "SELECT COUNT(*) FROM alertas WHERE lote_id='${LOTE}' AND tipo='PRECALENTAMIENTO_TIMEOUT'")"
check_eq "$ALERTAS" "0" "sin alerta PRECALENTAMIENTO_TIMEOUT"

echo
if [ "$FAILED" -ne 0 ]; then
  echo "RESULTADO: ${PASSED} OK, ${FAILED} FAIL"
  show_logs
  exit 1
fi
echo "RESULTADO: ${PASSED} OK, 0 FAIL"
