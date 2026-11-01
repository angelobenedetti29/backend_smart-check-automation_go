"""HTTP entrypoint del sidecar predictor (SCA-400).

Endpoints:
  GET  /health                       -> estado del sidecar
  POST /predict                      -> triplete (pct_crudos, pct_ok, pct_quemados)
  POST /admin/recargar/{producto_id} -> fuerza recargar el modelo de un producto
  GET  /admin/modelos                -> lista IDs de producto con modelo

El backend Go llama a POST /predict por cada decisión del MPC. El
servicio responde en <100 ms con el placeholder o con el XGBoost real
cuando ya existe modelo para el `producto_id`.
"""

from __future__ import annotations

import logging
from threading import Lock
from typing import Any

from fastapi import FastAPI, HTTPException

from .modelo import (
    ModeloPlaceholder,
    ModeloXGB,
    ProductoPlaceholder,
    cargar_modelo_para_producto,
    info_sidecar,
)
from .schemas import PredictRequest, PredictResponse

logger = logging.getLogger(__name__)

app = FastAPI(
    title="Smart-Check Predictor (SCA-400)",
    description="Sidecar XGBoost per-producto para el MPC del horno.",
)

# Cache de modelos por producto. Se invalida al recibir un POST /admin/recargar.
_cache: dict[str, tuple[Any, str, bool]] = {}
_cache_lock = Lock()


def _placeholder_de_request(req: PredictRequest) -> ProductoPlaceholder:
    """Snapshot mínimo del producto que necesita el placeholder.

    Usa los rangos que venga en el request; si faltan, defaults seguros.
    """
    return ProductoPlaceholder(
        id=req.producto_id,
        nombre="(desconocido)",
        temp_min=req.temp_min if req.temp_min is not None else 150.0,
        temp_max=req.temp_max if req.temp_max is not None else 230.0,
        velocidad_cinta_min=(
            req.velocidad_cinta_min if req.velocidad_cinta_min is not None else 0.05
        ),
        velocidad_cinta_max=(
            req.velocidad_cinta_max if req.velocidad_cinta_max is not None else 0.50
        ),
    )


@app.get("/health")
def health() -> dict:
    """Snapshot para que el backend sepa que el sidecar está vivo."""
    snap = info_sidecar()
    snap["status"] = "ok"
    return snap


@app.post("/predict", response_model=PredictResponse)
def predict(req: PredictRequest) -> PredictResponse:
    """Devuelve (pct_crudos, pct_ok, pct_quemados) para el producto y config."""
    with _cache_lock:
        cached = _cache.get(req.producto_id)

    placeholder = _placeholder_de_request(req)

    if cached is None:
        modelo, version, es_placeholder = cargar_modelo_para_producto(
            req.producto_id, placeholder
        )
        with _cache_lock:
            _cache[req.producto_id] = (modelo, version, es_placeholder)
    else:
        modelo, version, es_placeholder = cached

    try:
        pred = modelo.predict(placeholder, req)
    except Exception as exc:  # noqa: BLE001 - el MPC del backend no debe caer
        logger.exception("predictor: error en predicción")
        # Caemos a placeholder si el XGBoost falla.
        if not es_placeholder:
            placeholder_pred = ModeloPlaceholder().predict(placeholder, req)
            return PredictResponse(
                pct_crudos=placeholder_pred.pct_crudos,
                pct_ok=placeholder_pred.pct_ok,
                pct_quemados=placeholder_pred.pct_quemados,
                model_version=f"{version}_fallback_placeholder",
                es_placeholder=True,
            )
        raise HTTPException(status_code=500, detail=str(exc)) from exc

    return PredictResponse(
        pct_crudos=pred.pct_crudos,
        pct_ok=pred.pct_ok,
        pct_quemados=pred.pct_quemados,
        model_version=version,
        es_placeholder=es_placeholder,
    )


@app.post("/admin/recargar/{producto_id}")
def recargar(producto_id: str) -> dict:
    """Invalida la cache de un producto y fuerza recargar del próximo /predict."""
    with _cache_lock:
        _cache.pop(producto_id, None)
    return {"producto_id": producto_id, "recargado": True}


@app.get("/admin/modelos")
def listar_modelos() -> dict:
    """Lista los IDs de producto con modelo XGBoost entrenado."""
    return {"productos": info_sidecar()["modelos_entrenados"]}


def main() -> None:
    """Punto de entrada CLI: uvicorn.run(app)."""
    import uvicorn

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    uvicorn.run(app, host="0.0.0.0", port=8090, log_level="info")


if __name__ == "__main__":
    main()