"""Tests del sidecar predictor XGBoost (F-B11).

Se ejecutan con::

    cd cmd/predictor_svc
    python -m pytest tests/test_predict.py -v

Contrato: los schemas Pydantic tienen `alias_generator=to_camel`, así que el
JSON viaja en camelCase (`pctCrudos`, `pctOk`, `pctQuemados`, `modelVersion`,
`esPlaceholder`) igual que `optimizador.PrediccionSalida` del backend Go. El
request acepta camelCase (alias) o snake_case (nombre del campo), por lo que
estos tests también cubren la aceptación de ambos.
"""

from __future__ import annotations

import os
import re
import sys

# Permite `from predictor_svc.main import app` cuando pytest corre desde
# cmd/predictor_svc: agrega `cmd/` (el padre del paquete) al sys.path.
_CMD_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
if _CMD_DIR not in sys.path:
    sys.path.insert(0, _CMD_DIR)

import pytest
from fastapi.testclient import TestClient

from predictor_svc.main import app

client = TestClient(app)

_MODELS_DIR = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "models_xgb"
)


def test_health():
    r = client.get("/health")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_predict_placeholder():
    """Sin modelos entrenados, debe responder con placeholder determinístico.

    Manda el request en camelCase (alias) y verifica que la respuesta use
    camelCase, con las mismas keys que decodifica el cliente Go.
    """
    r = client.post(
        "/predict",
        json={
            "productoId": "prod-test",
            "tempHorno1": 180.0,
            "velocidadCinta": 0.45,
        },
    )
    assert r.status_code == 200
    data = r.json()
    for key in ("pctCrudos", "pctOk", "pctQuemados"):
        assert key in data, f"falta {key} en {data}"
    # Regresión de contrato: las keys snake_case NO deben aparecer.
    for key in ("pct_crudos", "pct_ok", "pct_quemados", "model_version", "es_placeholder"):
        assert key not in data, f"key snake_case inesperada {key!r} en {data}"
    total = data["pctCrudos"] + data["pctOk"] + data["pctQuemados"]
    assert abs(total - 100.0) < 0.1, f"los porcentajes no suman ~100: {data}"
    assert data["esPlaceholder"] is True


def test_predict_acepta_snake_case():
    """El request sigue aceptando snake_case (populate_by_name=True)."""
    r = client.post(
        "/predict",
        json={
            "producto_id": "prod-snake",
            "temp_horno_1": 180.0,
            "velocidad_cinta": 0.45,
        },
    )
    assert r.status_code == 200
    assert "pctCrudos" in r.json()


def test_predict_xgboost_si_hay_modelos():
    """Si models_xgb/ tiene booster para el producto, debe usar XGBoost real."""
    if not os.path.isdir(_MODELS_DIR):
        pytest.skip("models_xgb/ no existe")
    model_files = [
        f
        for f in os.listdir(_MODELS_DIR)
        if f.startswith("producto_") and f.endswith(".json")
    ]
    if not model_files:
        pytest.skip("no hay modelos XGBoost entrenados")

    m = re.match(r"producto_([0-9a-fA-F-]+)_v\d+\.json", model_files[0])
    if not m:
        pytest.skip(f"nombre de modelo inesperado: {model_files[0]}")
    producto_id = m.group(1)

    r = client.post(
        "/predict",
        json={
            "productoId": producto_id,
            "tempHorno1": 180.0,
            "velocidadCinta": 0.45,
        },
    )
    assert r.status_code == 200
    data = r.json()
    assert data.get("esPlaceholder") is False, "debería usar XGBoost real, no placeholder"


def test_predict_con_rangos_del_producto():
    """El request puede traer los rangos del producto; el placeholder los usa."""
    r = client.post(
        "/predict",
        json={
            "productoId": "prod-rangos",
            "tempHorno1": 190.0,
            "velocidadCinta": 0.30,
            "tempMin": 170.0,
            "tempMax": 210.0,
            "velocidadCintaMin": 0.20,
            "velocidadCintaMax": 0.40,
        },
    )
    assert r.status_code == 200
    data = r.json()
    assert "esPlaceholder" in data
    total = data["pctCrudos"] + data["pctOk"] + data["pctQuemados"]
    assert abs(total - 100.0) < 0.1, f"los porcentajes no suman ~100: {data}"


def test_predict_invalid_payload():
    """Un payload sin los campos requeridos debe ser rechazado (422)."""
    r = client.post("/predict", json={"productoId": ""})
    assert r.status_code in (400, 422)


def test_modelo_xgb_recorta_predicciones_fuera_de_rango():
    """Un booster puede salirse de [0, 100]; la respuesta debe quedar válida."""
    pytest.importorskip("xgboost")
    from types import SimpleNamespace

    from predictor_svc.modelo import ModeloXGB, ProductoPlaceholder

    class _Booster:
        def __init__(self, value: float) -> None:
            self.value = value

        def predict(self, _dmat):
            return [self.value]

    producto = ProductoPlaceholder(
        id="prod-test",
        nombre="Producto",
        temp_min=180.0,
        temp_max=220.0,
        velocidad_cinta_min=0.5,
        velocidad_cinta_max=1.5,
    )
    req = SimpleNamespace(
        temp_horno_1=185.0,
        velocidad_cinta=1.4,
        temp_comb_1=144.0,
        humedad_horno=35.0,
        temp_min=180.0,
        temp_max=220.0,
        velocidad_cinta_min=0.5,
        velocidad_cinta_max=1.5,
    )
    modelo = ModeloXGB(_Booster(120.0), _Booster(-5.0), _Booster(1.0), "test")

    pred = modelo.predict(producto, req)

    for value in (pred.pct_crudos, pred.pct_ok, pred.pct_quemados):
        assert 0.0 <= value <= 100.0
    assert abs(pred.pct_crudos + pred.pct_ok + pred.pct_quemados - 100.0) < 0.01
