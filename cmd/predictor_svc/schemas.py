"""Schemas Pydantic del servicio predictor (SCA-400).

Las entradas son floats; las salidas son floats normalizados a [0, 100] (el
backend Go los guarda como NUMERIC(5,2)). El placeholder puede devolver
cualquier triplete válido; la suma debe ser ~100 por construcción.

Contrato JSON en camelCase: los campos Python quedan en snake_case por
convención, pero el alias de serialización es camelCase (`pctCrudos`, `pctOk`,
`pctQuemados`, `modelVersion`, `esPlaceholder`) para coincidir con
`optimizador.PrediccionSalida` y `decisiones_control.PrediccionResumen` del
backend Go. Con `populate_by_name=True` el request también acepta snake_case
(compatibilidad hacia atrás).
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field
from pydantic.alias_generators import to_camel

# Configuración común: alias camelCase para el JSON y aceptación de los nombres
# Python (snake_case) en el request.
_CAMEL = ConfigDict(alias_generator=to_camel, populate_by_name=True)


class PredictRequest(BaseModel):
    """Cuerpo de POST /predict."""

    model_config = _CAMEL

    producto_id: str = Field(..., description="UUID del producto")
    temp_horno_1: float = Field(..., ge=0, le=600, description="°C del horno 1")
    temp_horno_2: float | None = Field(
        default=None, ge=0, le=600, description="°C del horno 2 (opcional)"
    )
    velocidad_cinta: float = Field(..., ge=0, le=10, description="m/s")
    temp_comb_1: float | None = Field(default=None, ge=0, le=1500)
    temp_comb_2: float | None = Field(default=None, ge=0, le=1500)
    humedad_horno: float | None = Field(default=None, ge=0, le=100)


class PredictResponse(BaseModel):
    """Respuesta de POST /predict (JSON en camelCase)."""

    model_config = _CAMEL

    pct_crudos: float = Field(..., ge=0, le=100)
    pct_ok: float = Field(..., ge=0, le=100)
    pct_quemados: float = Field(..., ge=0, le=100)
    model_version: str = Field(..., description="Etiqueta del modelo que produjo la respuesta")
    es_placeholder: bool = Field(..., description="True cuando no hay modelo entrenado y se usó el placeholder")