"""Tests de `train.py --dataset` (SCA-400): lee ventanas sin tocar la BD."""

from __future__ import annotations

import os
import sys
from pathlib import Path

_CMD_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
if _CMD_DIR not in sys.path:
    sys.path.insert(0, _CMD_DIR)

import pytest

from predictor_svc import train
from predictor_svc.export_dataset import COLUMNAS

_FILA_BASE = {
    "lote_id": "lote-1",
    "ventana_inicio": "2026-01-01T08:00:00+00:00",
    "ventana_fin": "2026-01-01T08:10:00+00:00",
    "temp_horno_1": "180.0",
    "velocidad_cinta": "0.30",
    "temp_comb_1": "500.0",
    "humedad_horno": "12.0",
    "temp_min": "150.0",
    "temp_max": "230.0",
    "vel_min": "0.05",
    "vel_max": "0.50",
    "pct_crudos": "10.0",
    "pct_ok": "80.0",
    "pct_quemados": "10.0",
    "n_eventos": "5",
    "confianza_promedio": "0.9",
}


def _escribir_csv(path: Path) -> Path:
    filas = [
        # desordenado a propósito: el loader ordena por inicio_at.
        {"producto_id": "prod-b", "inicio_at": "2026-01-01T09:00:00+00:00"},
        {"producto_id": "prod-a", "inicio_at": "2026-01-01T08:00:00+00:00"},
        {"producto_id": "prod-a", "inicio_at": "2026-01-02T08:00:00+00:00"},
    ]
    lineas = [",".join(COLUMNAS)]
    for extra in filas:
        fila = {**_FILA_BASE, **extra}
        lineas.append(",".join(str(fila[c]) for c in COLUMNAS))
    path.write_text("\n".join(lineas) + "\n", encoding="utf-8")
    return path


def test_carga_dataset_y_agrupa_por_producto(tmp_path):
    muestras = train.cargar_muestras_dataset(_escribir_csv(tmp_path / "train.csv"))

    assert len(muestras) == 3
    # Orden cronológico por inicio_at, no orden de filas.
    assert [m.producto_id for m in muestras] == ["prod-a", "prod-b", "prod-a"]

    por_producto = train.agrupar_por_producto(muestras)
    assert set(por_producto) == {"prod-a", "prod-b"}
    assert len(por_producto["prod-a"]) == 2
    assert len(por_producto["prod-b"]) == 1


def test_carga_dataset_falta_columna(tmp_path):
    path = tmp_path / "roto.csv"
    path.write_text("producto_id,temp_horno_1\nprod-a,180\n", encoding="utf-8")
    with pytest.raises(ValueError, match="faltan columnas"):
        train.cargar_muestras_dataset(path)


def test_main_con_dataset_no_consulta_la_bd(tmp_path, monkeypatch):
    path = _escribir_csv(tmp_path / "train.csv")
    monkeypatch.delenv("DATABASE_URL", raising=False)

    def boom(_url):
        raise AssertionError("con --dataset no debe leer la BD")

    entrenados: list[str] = []

    def falso_entrenar(muestras, min_muestras, test_size):
        entrenados.append(muestras[0].producto_id)
        return None

    monkeypatch.setattr(train, "cargar_muestras", boom)
    monkeypatch.setattr(train, "entrenar_producto", falso_entrenar)

    assert train.main(["--dataset", str(path), "--min-muestras", "1"]) == 0
    assert entrenados == ["prod-a", "prod-b"]


def test_main_sin_dataset_sigue_leyendo_la_bd(monkeypatch):
    """Sin --dataset la ruta actual no cambia: consulta la BD y exige URL."""
    monkeypatch.setenv("DATABASE_URL", "postgres://fake/db")
    llamadas: list[str] = []

    def falso_cargar(url: str):
        llamadas.append(url)
        return []

    monkeypatch.setattr(train, "cargar_muestras", falso_cargar)

    assert train.main([]) == 0
    assert llamadas == ["postgres://fake/db"]

    monkeypatch.delenv("DATABASE_URL", raising=False)
    assert train.main([]) == 1
    assert llamadas == ["postgres://fake/db"]
