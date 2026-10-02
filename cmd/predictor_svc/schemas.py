"""Schemas Pydantic del servicio predictor (SCA-400).

Las entradas son floats; las salidas son floats normalizados a [0, 100] (el
backend Go los guarda como NUMERIC(5,2)). El placeholder puede devolver
cualquier triplete válido; la suma debe ser ~100 por construcción.
"""

from __future__ import annotations

from pydantic import BaseModel, Field


class PredictRequest(BaseModel):
    """Cuerpo de POST /predict."""

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
    """Respuesta de POST /predict."""

    pct_crudos: float = Field(..., ge=0, le=100)
    pct_ok: float = Field(..., ge=0, le=100)
    pct_quemados: float = Field(..., ge=0, le=100)
    model_version: str = Field(..., description="Etiqueta del modelo que produjo la respuesta")
    es_placeholder: bool = Field(..., description="True cuando no hay modelo entrenado y se usó el placeholder")