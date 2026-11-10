"""Constructor compartido del vector de features (SCA-400).

Entrenamiento (`train.py`) y serving (`modelo.py`) DEBEN usar esta misma
función: si la normalización difiere entre ambos, el modelo entrenado ve
features que no coinciden con las de inferencia y la predicción es basura.

Orden estable del vector (6 elementos):
    [temp, velocidad, temp_comb|nan, humedad|nan, d_t, d_v]

Normalización: centro = (min + max) / 2 y ancho = max(1, max-min) para
temperatura, max(0.01, max-min) para velocidad. Nunca se usa un setpoint.
"""

from __future__ import annotations


def construir_features(
    temp: float,
    velocidad: float,
    temp_comb: float | None,
    humedad: float | None,
    temp_min: float,
    temp_max: float,
    vel_min: float,
    vel_max: float,
) -> list[float]:
    """Devuelve el vector de 6 features para entrenar y predecir por igual."""
    centro_t = (temp_max + temp_min) / 2.0
    ancho_t = max(1.0, temp_max - temp_min)
    centro_v = (vel_max + vel_min) / 2.0
    ancho_v = max(0.01, vel_max - vel_min)
    d_t = (temp - centro_t) / ancho_t
    d_v = (velocidad - centro_v) / ancho_v
    return [
        temp,
        velocidad,
        temp_comb if temp_comb is not None else float("nan"),
        humedad if humedad is not None else float("nan"),
        d_t,
        d_v,
    ]
