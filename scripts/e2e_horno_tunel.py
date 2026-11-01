#!/usr/bin/env python3
"""Driver E2E del flujo de control de un horno túnel (sólo stdlib).

Simula dos nodos Raspberry (ENTRADA_HORNO y SALIDA_HORNO) del sector 'e2e'
hablando el contrato HTTP real contra el backend en ejecución:

  1. abre lote en AUTOMATICO con estado inicial (arranque manual enganchado)
  2. publica telemetría del horno y detecciones de la salida
  3. espera la propuesta del MPC (polling con deadline, sin sleeps fijos)
  4. confirma la consigna (ack) y verifica que el lote espeje los setpoints
  5. cierra el lote con los conteos de los eventos publicados

Cada aserción imprime una línea OK/FAIL. Sale con código != 0 en la primera
falla, incluyendo el body de la respuesta.
"""
import argparse
import json
import sys
import time
import uuid
from datetime import datetime, timezone
from urllib import error, request

PRODUCTO_DEFAULT = "11111111-1111-4111-8111-111111111111"


class Fail(Exception):
    pass


def now_iso():
    return datetime.now(timezone.utc).isoformat()


def call(base, method, path, token=None, body=None, timeout=15):
    data = json.dumps(body).encode() if body is not None else None
    req = request.Request(base + path, data=data, method=method)
    if token:
        req.add_header("Authorization", "Bearer " + token)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None), raw
    except error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            parsed = json.loads(raw)
        except ValueError:
            parsed = None
        return exc.code, parsed, raw


def check(cond, label, detail=""):
    if cond:
        print("  OK   " + label)
        return
    print("  FAIL " + label + ((": " + str(detail)) if detail else ""))
    raise Fail(label)


def approx(value, expected, tol=1e-6):
    try:
        return abs(float(value) - float(expected)) <= tol
    except (TypeError, ValueError):
        return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:18080")
    parser.add_argument("--entrada-secret", required=True)
    parser.add_argument("--salida-secret", required=True)
    parser.add_argument("--producto-id", default=PRODUCTO_DEFAULT)
    args = parser.parse_args()

    base = args.base_url.rstrip("/")
    entrada = args.entrada_secret
    salida = args.salida_secret
    producto = args.producto_id
    conteos = {"ok": 0, "crudo": 0, "quemado": 0}

    # --- 1. Abrir el lote en AUTOMATICO con estado inicial -------------------
    inicio = {
        "idempotency_key": str(uuid.uuid4()),
        "producto_id": producto,
        "momento": now_iso(),
        "modo": "AUTOMATICO",
        "iniciado_manualmente": True,
        "estado_inicial": {
            "temp_horno_1": 200,
            "velocidad_cinta": 0.4,
            "temp_objetivo": 200,
            "vel_objetivo": 0.4,
        },
        "ventana_evaluacion_segundos": 6,
        "ventana_actualizacion_segundos": 2,
        "tolerancia_temp_c": 3,
        "umbral_aceptacion_crudo": 5,
        "umbral_aceptacion_quemado": 5,
    }
    status, payload, raw = call(base, "POST", "/api/v1/lotes/inicio", entrada, inicio)
    check(status == 201, "POST /lotes/inicio -> 201", raw)
    lote = (payload or {}).get("data", {}).get("lote") or {}
    lote_id = lote.get("id")
    check(lote.get("estado") == "ABIERTO", "lote.estado == ABIERTO", raw)
    check(approx(lote.get("temp1_objetivo"), 200), "lote.temp1_objetivo == 200", raw)
    check(approx(lote.get("vel_objetivo"), 0.4), "lote.vel_objetivo == 0.4", raw)
    check(bool(lote_id), "lote.id presente", raw)

    # --- 2. Telemetría + eventos de la salida durante ~8 s -------------------
    def post_metrica(idx):
        body = {
            "temp_horno_1": 200.0 + (idx % 6),
            "velocidad_cinta": 0.4,
            "temp_comb_1": 210.0,
            "humedad_horno": 35.0,
            "temp_objetivo": 200.0,
            "vel_objetivo": 0.4,
            "received_at": now_iso(),
        }
        st, js, rw = call(
            base, "POST", "/api/v1/lotes/%s/metricas-horno" % lote_id, entrada, body
        )
        check(st == 201, "POST metricas-horno #%d -> 201" % (idx + 1), rw)

    def post_eventos(estados):
        eventos = [
            {
                "evento_id": str(uuid.uuid4()),
                "producto_id": producto,
                "estado": est,
                "confianza": 0.9,
                "momento": now_iso(),
            }
            for est in estados
        ]
        st, js, rw = call(
            base,
            "POST",
            "/api/v1/lotes/%s/eventos" % lote_id,
            salida,
            {"eventos": eventos},
        )
        check(st == 200, "POST eventos %s -> 200" % ",".join(estados), rw)
        data = (js or {}).get("data") or {}
        check(
            data.get("aceptados") == len(estados),
            "eventos aceptados == %d" % len(estados),
            rw,
        )
        for est in estados:
            conteos[est] += 1

    for i in range(9):
        post_metrica(i)
        if i == 2:
            post_eventos(["ok", "ok", "ok", "crudo"])
        if i == 5:
            post_eventos(["ok", "quemado", "ok"])
        if i < 8:
            time.sleep(1)

    # --- 3. Polling de la propuesta del MPC (deadline, no sleeps fijos) ------
    deadline = time.time() + 20
    propuesta = None
    next_metric = time.time() + 1.0
    idx = 9
    while time.time() < deadline:
        # Mantener telemetría fresca dentro de la ventana de evaluación (6 s).
        if time.time() >= next_metric:
            post_metrica(idx)
            idx += 1
            next_metric = time.time() + 1.0
        st, js, rw = call(
            base,
            "GET",
            "/api/v1/lotes/%s/consigna-propuesta" % lote_id,
            entrada,
        )
        if st == 200 and js and js.get("data"):
            data = js["data"]
            if data.get("autorizada") is True and data.get("decision_id"):
                propuesta = data
                break
        elif st not in (200, 204):
            check(False, "GET consigna-propuesta -> 200/204", rw)
        time.sleep(0.5)

    check(propuesta is not None, "GET consigna-propuesta -> autorizada (<=20 s)")
    check(propuesta.get("fase") == "ADAPTANDO", "propuesta.fase == ADAPTANDO", propuesta)
    limites = propuesta.get("limites") or {}
    check(approx(limites.get("temp_min"), 180), "limites.temp_min == 180", limites)
    check(approx(limites.get("temp_max"), 220), "limites.temp_max == 220", limites)
    check(approx(limites.get("vel_min"), 0.1), "limites.vel_min == 0.1", limites)
    check(approx(limites.get("vel_max"), 0.6), "limites.vel_max == 0.6", limites)
    check(
        approx(limites.get("delta_temp1_max_c"), 10),
        "limites.delta_temp1_max_c == 10",
        limites,
    )
    check(
        approx(limites.get("delta_vel_max"), 0.05),
        "limites.delta_vel_max == 0.05",
        limites,
    )

    # --- 4. Ack de la consigna + espejo de setpoints en el lote --------------
    temp_aplicado = float(propuesta["temp1_objetivo"])
    vel_aplicado = float(propuesta["vel_objetivo"])
    decision_id = propuesta["decision_id"]
    st, js, rw = call(
        base,
        "POST",
        "/api/v1/lotes/%s/consigna/ack" % lote_id,
        entrada,
        {
            "decision_id": decision_id,
            "temp_aplicado": temp_aplicado,
            "vel_aplicado": vel_aplicado,
        },
    )
    check(st == 200, "POST consigna/ack -> 200", rw)
    ack_data = (js or {}).get("data") or {}
    check(ack_data.get("aplicado") is True, "ack.aplicado == true", rw)
    check(ack_data.get("decision_id") == decision_id, "ack.decision_id coincide", rw)

    # La API de lote expone el ABIERTO del sector del device (no hay GET /lotes/{id}).
    st, js, rw = call(base, "GET", "/api/v1/lotes/abierto", entrada)
    check(st == 200, "GET /lotes/abierto -> 200", rw)
    abierto = (js or {}).get("data", {}).get("lote") or {}
    check(abierto.get("id") == lote_id, "lote abierto == lote del E2E", rw)
    check(
        approx(abierto.get("temp1_objetivo"), temp_aplicado),
        "lote.temp1_objetivo == temp_aplicado (%.2f)" % temp_aplicado,
        rw,
    )
    check(
        approx(abierto.get("vel_objetivo"), vel_aplicado),
        "lote.vel_objetivo == vel_aplicado (%.3f)" % vel_aplicado,
        rw,
    )

    # --- 5. Cierre con los conteos de los eventos publicados -----------------
    total = sum(conteos.values())
    st, js, rw = call(
        base,
        "POST",
        "/api/v1/lotes/%s/cierre" % lote_id,
        entrada,
        {
            "idempotency_key": str(uuid.uuid4()),
            "motivo": "manual",
            "conteos": {
                "ok": conteos["ok"],
                "crudo": conteos["crudo"],
                "quemado": conteos["quemado"],
                "total": total,
            },
        },
    )
    check(st == 200, "POST cierre -> 200", rw)
    check((js or {}).get("data", {}).get("cerrado") is True, "cierre.cerrado == true", rw)

    print("  OK   flujo completo: lote %s" % lote_id)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Fail:
        sys.exit(1)
    except Exception as exc:  # noqa: BLE001 - queremos el mensaje en el reporte
        print("  FAIL excepción no controlada: %r" % (exc,))
        sys.exit(1)
