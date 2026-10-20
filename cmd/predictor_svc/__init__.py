"""Servicio predictor XGBoost per-producto (SCA-400 / SCA-142 reactivado).

Es un sidecar HTTP al que el backend Go llama vía HTTP. Mantiene un modelo
por producto_id; cuando no hay modelo todavía, devuelve un placeholder
calibrado. La razón de esto es arrancar con algo útil sin histórico
(la planta recién empieza a registrar datos) y mejorar a medida que se
acumulan lotes.

El predictor por producto es crítico porque el modelo de visión cambia de
clase (tostada, pan francés, medialuna...) y la dinámica térmica del horno
depende del producto. Un modelo global estaría promediando dinámicas que
no tienen relación entre sí.

Este módulo se ejecuta con:
    uvicorn cmd.predictor_svc.main:app --host 0.0.0.0 --port 8090

Las dependencias se instalan con:
    pip install -r cmd/predictor_svc/requirements.txt
"""

# Importante: este archivo vive en backend_smart-check-automation_go/cmd/predictor_svc/.
# Se ejecuta como servicio aparte, no se importa desde el backend Go.

from .modelo import (
    ModeloPlaceholder,
    ModeloXGB,
    cargar_modelo_para_producto,
    listar_productos_con_modelo,
)
from .schemas import (
    PredictRequest,
    PredictResponse,
)

__all__ = [
    "ModeloPlaceholder",
    "ModeloXGB",
    "cargar_modelo_para_producto",
    "listar_productos_con_modelo",
    "PredictRequest",
    "PredictResponse",
]