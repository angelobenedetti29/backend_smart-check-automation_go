"""Tests puros del export de ventanas (SCA-400). Sin DB ni dependencias extra."""

from __future__ import annotations

import csv
import json
import os
import sys
from datetime import datetime, timedelta, timezone

import pytest

_CMD_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
if _CMD_DIR not in sys.path:
    sys.path.insert(0, _CMD_DIR)

from predictor_svc.export_dataset import (
    COLUMNAS,
    Evento,
    Lote,
    Metrica,
    agregar_etiquetas,
    construir_filas_lote,
    construir_manifest,
    exportar,
    generar_ventanas,
    escribir_dataset,
)

UTC = timezone.utc
T0 = datetime(2026, 1, 1, 8, 0, tzinfo=UTC)


def _lote(inicio: datetime, fin: datetime, eval_s: int | None = 600,
          actual_s: int | None = 300) -> Lote:
    return Lote(
        id="lote-1",
        producto_id="prod-1",
        inicio_at=inicio,
        fin_at=fin,
        ventana_evaluacion_segundos=eval_s,
        ventana_actualizacion_segundos=actual_s,
        temp_min=150.0,
        temp_max=230.0,
        vel_min=0.05,
        vel_max=0.50,
    )


def _eventos_en_ventana(inicio: datetime) -> list[Evento]:
    return [
        Evento(inicio + timedelta(seconds=10), "ok", 0.9),
        Evento(inicio + timedelta(seconds=20), "crudo", 0.8),
        Evento(inicio + timedelta(seconds=30), None, 0.5),
    ]


def _validos(inicio: datetime, n: int) -> list[Evento]:
    return [
        Evento(inicio + timedelta(seconds=40 + i), "ok", 0.9) for i in range(n)
    ]


# --- ventanas ---------------------------------------------------------------


def test_ventanas_solapadas_por_paso():
    fin = T0 + timedelta(seconds=1800)
    ventanas = generar_ventanas(T0, fin, 600, 300)

    assert len(ventanas) == 6
    assert ventanas[0][0] == T0
    # paso 300 < largo 600: se solapan 300 s.
    assert ventanas[1][0] == ventanas[0][0] + timedelta(seconds=300)
    assert ventanas[1][0] < ventanas[0][1]
    assert all(
        b[0] - a[0] == timedelta(seconds=300)
        for a, b in zip(ventanas, ventanas[1:])
    )


def test_ventanas_recortan_en_fin_at():
    # Largo de lote no múltiplo del paso: la última ventana termina en fin_at.
    fin = T0 + timedelta(seconds=1300)
    ventanas = generar_ventanas(T0, fin, 600, 300)

    assert ventanas[-1][1] == fin
    assert ventanas[-1][0] < fin
    assert all(inicio < fin for inicio, _ in ventanas)
    assert not any(inicio == fin for inicio, _ in ventanas)


def test_ventanas_defaults_y_lote_vacio():
    # NULL/<=0 caen a 600/300.
    ventanas = generar_ventanas(T0, T0 + timedelta(seconds=600), None, 0)
    assert len(ventanas) == 2
    assert ventanas[0][1] - ventanas[0][0] == timedelta(seconds=600)
    assert generar_ventanas(T0, T0, 600, 300) == []


# --- etiquetas --------------------------------------------------------------


def test_etiquetas_excluyen_estado_null_del_denominador():
    fin = T0 + timedelta(seconds=600)
    eventos = _eventos_en_ventana(T0) + [
        Evento(T0 + timedelta(seconds=40), "quemado", None),
        Evento(T0 - timedelta(seconds=1), "ok", 0.9),      # antes: fuera
        Evento(fin, "ok", 0.9),                            # borde: fuera
    ]
    etiquetas = agregar_etiquetas(eventos, T0, fin)

    assert etiquetas is not None
    assert etiquetas["n_eventos"] == 3  # el NULL no suma al denominador
    assert abs(etiquetas["pct_ok"] - 100 / 3) < 1e-9
    assert abs(etiquetas["pct_crudos"] - 100 / 3) < 1e-9
    assert abs(etiquetas["pct_quemados"] - 100 / 3) < 1e-9
    assert etiquetas["confianza_promedio"] == pytest.approx(0.85)  # (0.9+0.8)/2


def test_etiquetas_sin_eventos_validos_es_none():
    assert agregar_etiquetas(
        [Evento(T0, None, 0.9)], T0, T0 + timedelta(seconds=600)
    ) is None


# --- min-eventos y filas ----------------------------------------------------


def test_min_eventos_descarta_ventana():
    fin = T0 + timedelta(seconds=600)
    lote = _lote(T0, fin)
    metricas = [Metrica(T0 + timedelta(seconds=30), 180.0, 0.30, 500.0, 12.0)]
    cuatro = _eventos_en_ventana(T0) + _validos(T0, 2)

    assert construir_filas_lote(lote, metricas, cuatro, min_eventos=5) == []
    filas = construir_filas_lote(lote, metricas, cuatro, min_eventos=4)
    assert len(filas) == 1
    assert filas[0]["n_eventos"] == 4


def test_ventana_sin_metrica_o_con_nulos_se_descarta():
    fin = T0 + timedelta(seconds=600)
    lote = _lote(T0, fin)
    eventos = _eventos_en_ventana(T0) + _validos(T0, 3)
    # Métrica posterior al fin de ventana: no sirve "as of".
    tarde = [Metrica(fin + timedelta(seconds=1), 180.0, 0.30, None, None)]
    assert construir_filas_lote(lote, tarde, eventos, min_eventos=5) == []

    sin_temp = [Metrica(T0, None, 0.30, None, None)]
    assert construir_filas_lote(lote, sin_temp, eventos, min_eventos=5) == []


def test_metrica_as_of_usa_la_ultima_anterior_al_fin():
    fin = T0 + timedelta(seconds=600)
    lote = _lote(T0, fin)
    metricas = [
        Metrica(T0 + timedelta(seconds=100), 170.0, 0.20, None, None),
        Metrica(T0 + timedelta(seconds=400), 190.0, 0.25, None, None),
        Metrica(fin + timedelta(seconds=1), 250.0, 0.90, None, None),
    ]
    filas = construir_filas_lote(
        lote, metricas, _eventos_en_ventana(T0) + _validos(T0, 3), min_eventos=5
    )

    assert len(filas) == 1
    assert filas[0]["temp_horno_1"] == 190.0
    assert filas[0]["ventana_inicio"] == T0
    assert filas[0]["ventana_fin"] == fin


# --- manifest y archivos ----------------------------------------------------


def test_manifest_shape():
    fin = T0 + timedelta(seconds=600)
    lote = _lote(T0, fin)
    filas = construir_filas_lote(
        lote,
        [Metrica(T0 + timedelta(seconds=30), 180.0, 0.30, 500.0, 12.0)],
        _eventos_en_ventana(T0) + _validos(T0, 3),
        min_eventos=5,
    )
    generado = datetime(2026, 1, 2, tzinfo=UTC)
    manifest = construir_manifest(
        filas, [lote], formato="csv", min_eventos=5, generado_en=generado
    )

    assert set(manifest) == {
        "generado_en",
        "ventana_evaluacion_segundos",
        "ventana_actualizacion_segundos",
        "min_eventos",
        "formato",
        "filas",
        "lotes",
        "productos",
        "politica_etiquetas",
        "schema_version",
    }
    assert manifest["generado_en"] == generado.isoformat()
    assert manifest["ventana_evaluacion_segundos"] == 600
    assert manifest["ventana_actualizacion_segundos"] == 300
    assert manifest["min_eventos"] == 5
    assert manifest["formato"] == "csv"
    assert manifest["filas"] == 1
    assert manifest["lotes"] == 1
    assert manifest["productos"] == {"prod-1": 1}
    assert manifest["schema_version"] == 1
    assert "NULL" in manifest["politica_etiquetas"]


def test_exportar_escribe_columnas_y_manifest(tmp_path):
    fin = T0 + timedelta(seconds=600)
    lote = _lote(T0, fin)
    metricas = {"lote-1": [Metrica(T0 + timedelta(seconds=30), 180.0, 0.30, 500.0, 12.0)]}
    eventos = {
        "lote-1": _eventos_en_ventana(T0) + _validos(T0, 3)
    }

    manifest = exportar(
        [lote], metricas, eventos,
        out_dir=tmp_path, formato="csv", min_eventos=5, split_temporal=0.8,
    )

    assert manifest["filas"] == 1
    with (tmp_path / "train.csv").open(encoding="utf-8", newline="") as fh:
        reader = csv.DictReader(fh)
        assert reader.fieldnames == COLUMNAS
        fila = next(reader)
    assert fila["producto_id"] == "prod-1"
    assert fila["lote_id"] == "lote-1"
    assert float(fila["confianza_promedio"]) == pytest.approx(0.88)

    # Split 0.8 con 1 fila: el corte mínimo deja todo en train.
    assert (tmp_path / "val.csv").exists()
    escrito = json.loads((tmp_path / "manifest.json").read_text(encoding="utf-8"))
    assert escrito["schema_version"] == 1
    assert escrito["productos"] == {"prod-1": 1}


def test_split_temporal_no_parte_un_lote(tmp_path):
    """El corte es por lote: las ventanas de un lote no se reparten train/val."""
    filas = []
    for lote, inicio in (("lote-a", T0), ("lote-b", T0 + timedelta(days=1))):
        for k in range(3):
            filas.append(
                {
                    "producto_id": "prod-1",
                    "lote_id": lote,
                    "inicio_at": inicio,
                    "ventana_inicio": inicio + timedelta(minutes=5 * k),
                    "ventana_fin": inicio + timedelta(minutes=5 * k + 10),
                    "temp_horno_1": 190.0 + k,
                    "velocidad_cinta": 0.3,
                    "temp_comb_1": 400.0,
                    "humedad_horno": 45.0,
                    "temp_min": 180.0,
                    "temp_max": 220.0,
                    "vel_min": 0.1,
                    "vel_max": 0.6,
                    "pct_crudos": 10.0,
                    "pct_ok": 80.0,
                    "pct_quemados": 10.0,
                    "n_eventos": 10,
                    "confianza_promedio": 0.8,
                }
            )

    escribir_dataset(filas, out_dir=tmp_path, formato="csv", split_temporal=0.5)

    def lotes(path):
        with path.open(encoding="utf-8", newline="") as fh:
            return {r["lote_id"] for r in csv.DictReader(fh)}

    assert lotes(tmp_path / "train.csv") == {"lote-a"}
    assert lotes(tmp_path / "val.csv") == {"lote-b"}
