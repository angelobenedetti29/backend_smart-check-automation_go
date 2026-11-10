"""Tests del constructor compartido de features (SCA-400).

Puros stdlib: no importan fastapi ni xgboost, por lo que corren aunque el
entorno de serving no esté instalado.
"""

from __future__ import annotations

import math
import os
import sys

_CMD_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
if _CMD_DIR not in sys.path:
    sys.path.insert(0, _CMD_DIR)

from predictor_svc.features import construir_features


def test_orden_de_features():
    f = construir_features(180.0, 0.40, 500.0, 12.0, 150.0, 230.0, 0.05, 0.50)
    assert len(f) == 6
    assert f[0] == 180.0
    assert f[1] == 0.40
    assert f[2] == 500.0
    assert f[3] == 12.0
    # centro_t = 190, ancho_t = 80; centro_v = 0.275, ancho_v = 0.45
    assert math.isclose(f[4], (180.0 - 190.0) / 80.0)
    assert math.isclose(f[5], (0.40 - 0.275) / 0.45)


def test_faltantes_van_a_nan():
    f = construir_features(180.0, 0.40, None, None, 150.0, 230.0, 0.05, 0.50)
    assert math.isnan(f[2])
    assert math.isnan(f[3])


def test_rango_mas_estrecho_mayor_d_t():
    ancho = construir_features(195.0, 0.30, None, None, 150.0, 230.0, 0.05, 0.50)
    angosto = construir_features(195.0, 0.30, None, None, 185.0, 195.0, 0.05, 0.50)
    assert abs(angosto[4]) > abs(ancho[4])


def test_rango_mas_estrecho_mayor_d_v():
    ancho = construir_features(180.0, 0.33, None, None, 150.0, 230.0, 0.05, 0.50)
    angosto = construir_features(180.0, 0.33, None, None, 150.0, 230.0, 0.28, 0.32)
    assert abs(angosto[5]) > abs(ancho[5])


def test_anchos_seguros_cuando_min_igual_max():
    f = construir_features(200.0, 0.30, None, None, 200.0, 200.0, 0.30, 0.30)
    # ancho_t = max(1, 0) = 1; ancho_v = max(0.01, 0) = 0.01
    assert f[4] == 0.0
    assert f[5] == 0.0

    f = construir_features(201.0, 0.35, None, None, 200.0, 200.0, 0.30, 0.30)
    assert f[4] == 1.0
    assert math.isclose(f[5], 5.0)
