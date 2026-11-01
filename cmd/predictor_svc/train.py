"""Entrenamiento de XGBoost per-producto (SCA-400).

Lee el dataset desde el backend Go (vía DATABASE_URL): agrega por lote el
% crudo, % ok y % quemado observados, y une con la telemetría del horno
(metricas_horno) y los parámetros del producto. Entrena tres XGBoost
(crudos, ok, quemados) por producto y los guarda en
`models_xgb/producto_{uuid}_v{n}.json`.

Uso:
    DATABASE_URL=postgres://... python -m cmd.predictor_svc.train
    DATABASE_URL=postgres://... python -m cmd.predictor_svc.train --producto a1b2...
    python -m cmd.predictor_svc.train --dataset data/windows/train.csv

La ruta con `--dataset` entrena con las ventanas exportadas por
`export_dataset.py` (una fila = una muestra punto-en-el-tiempo). Sin
`--dataset` se mantiene el agregado por lote histórico.

Este script es batch: se corre bajo demanda cuando se acumula histórico
suficiente (default: ≥30 muestras por producto). El MPC sigue funcionando
con el placeholder mientras tanto.

Si xgboost no está instalado, el script degrada a "skip" sin entrenar.
"""

from __future__ import annotations

import argparse
import csv
import json
import logging
import os
import re
import sys
from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Iterable

try:
    import xgboost as xgb  # type: ignore
except ImportError:
    xgb = None

from .features import construir_features

try:
    import psycopg2  # type: ignore
    import psycopg2.extras  # type: ignore
except ImportError:
    psycopg2 = None

logger = logging.getLogger(__name__)

DEFAULT_MIN_MUESTRAS = 30
DEFAULT_TEST_SIZE = 0.2


@dataclass(frozen=True, slots=True)
class MuestraLote:
    """Una fila de entrenamiento: features + targets por lote."""

    producto_id: str
    temp_horno_1: float
    velocidad_cinta: float
    temp_comb_1: float | None
    humedad_horno: float | None
    pct_crudos: float
    pct_ok: float
    pct_quemados: float
    temp_min: float
    temp_max: float
    vel_min: float
    vel_max: float


def _directorio_modelos() -> Path:
    return Path(__file__).resolve().parent / "models_xgb"


def _version_actual(modelo_id: str) -> int:
    base = _directorio_modelos()
    if not base.is_dir():
        return 0
    versiones = []
    for path in base.glob(f"producto_{modelo_id}_v*.json"):
        m = re.search(r"_v(\d+)\.json$", path.name)
        if m:
            versiones.append(int(m.group(1)))
    return max(versiones) if versiones else 0


def _features_de_muestra(m: MuestraLote) -> list[float]:
    return construir_features(
        m.temp_horno_1,
        m.velocidad_cinta,
        m.temp_comb_1,
        m.humedad_horno,
        m.temp_min,
        m.temp_max,
        m.vel_min,
        m.vel_max,
    )


def _targets(m: MuestraLote) -> tuple[float, float, float]:
    return m.pct_crudos, m.pct_ok, m.pct_quemados


def cargar_muestras(database_url: str) -> list[MuestraLote]:
    """Carga y agrega el dataset desde la BD para todos los productos.

    Una muestra = una fila por lote con los % agregados y las lecturas
    promedio del horno. Sólo entran lotes con al menos un evento de
    salida (los que no, son ruido).
    """
    if psycopg2 is None:
        raise RuntimeError(
            "psycopg2 no está instalado: pip install psycopg2-binary"
        )

    sql = """
    WITH
      params AS (
        SELECT id, temp_min, temp_max,
               velocidad_cinta_min, velocidad_cinta_max
        FROM productos
      ),
      obs AS (
        SELECT
          lote_id, producto_id,
          COUNT(*) FILTER (WHERE estado = 'crudo')   AS n_crudos,
          COUNT(*) FILTER (WHERE estado = 'ok')      AS n_ok,
          COUNT(*) FILTER (WHERE estado = 'quemado') AS n_quemados,
          COUNT(*) AS total
        FROM eventos_lote
        GROUP BY lote_id, producto_id
      )
    SELECT
      l.id, l.producto_id,
      AVG(m.temp_horno_1) AS temp_horno_1,
      AVG(m.velocidad_cinta) AS velocidad_cinta,
      AVG(m.temp_comb_1) AS temp_comb_1,
      AVG(m.humedad_horno) AS humedad_horno,
      100.0 * obs.n_crudos   / obs.total AS pct_crudos,
      100.0 * obs.n_ok       / obs.total AS pct_ok,
      100.0 * obs.n_quemados / obs.total AS pct_quemados,
      p.temp_min, p.temp_max,
      p.velocidad_cinta_min, p.velocidad_cinta_max
    FROM lotes_productivos l
    JOIN obs ON obs.lote_id = l.id
    JOIN params p ON p.id = l.producto_id
    LEFT JOIN metricas_horno m ON m.lote_id = l.id
    WHERE obs.total > 0 AND l.estado = 'CERRADO'
    GROUP BY l.id, l.producto_id, obs.n_crudos, obs.n_ok, obs.n_quemados, obs.total,
             p.temp_min, p.temp_max, p.velocidad_cinta_min, p.velocidad_cinta_max
    ORDER BY l.id
    """
    conn = psycopg2.connect(database_url)
    cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
    cur.execute(sql)
    out: list[MuestraLote] = []
    for row in cur.fetchall():
        # AVG sobre todas las métricas devuelve null si no hay métricas.
        if row["temp_horno_1"] is None or row["velocidad_cinta"] is None:
            continue
        out.append(
            MuestraLote(
                producto_id=str(row["producto_id"]),
                temp_horno_1=float(row["temp_horno_1"]),
                velocidad_cinta=float(row["velocidad_cinta"]),
                temp_comb_1=_opt_float(row["temp_comb_1"]),
                humedad_horno=_opt_float(row["humedad_horno"]),
                pct_crudos=float(row["pct_crudos"]),
                pct_ok=float(row["pct_ok"]),
                pct_quemados=float(row["pct_quemados"]),
                temp_min=float(row["temp_min"]),
                temp_max=float(row["temp_max"]),
                vel_min=float(row["velocidad_cinta_min"]),
                vel_max=float(row["velocidad_cinta_max"]),
            )
        )
    cur.close()
    conn.close()
    return out


def _opt_float(x: object) -> float | None:
    if x is None:
        return None
    return float(x)


def _leer_filas_dataset(path: Path) -> list[dict[str, Any]]:
    if path.suffix.lower() == ".csv":
        with path.open(encoding="utf-8", newline="") as fh:
            reader = csv.DictReader(fh)
            return list(reader)
    filas: list[dict[str, Any]] = []
    with path.open(encoding="utf-8") as fh:
        for linea in fh:
            if linea.strip():
                filas.append(json.loads(linea))
    return filas


def _numero(valor: object) -> float | None:
    if valor is None or valor == "":
        return None
    try:
        return float(valor)
    except (TypeError, ValueError):
        return None


def _inicio_at(valor: object) -> datetime | None:
    if not isinstance(valor, str) or not valor:
        return None
    try:
        return datetime.fromisoformat(valor)
    except ValueError:
        return None


_CAMPOS_DATASET = (
    "producto_id",
    "temp_horno_1",
    "velocidad_cinta",
    "pct_crudos",
    "pct_ok",
    "pct_quemados",
    "temp_min",
    "temp_max",
    "vel_min",
    "vel_max",
)


def cargar_muestras_dataset(path: Path) -> list[MuestraLote]:
    """Lee las ventanas exportadas por `export_dataset.py` (csv o jsonl).

    Una fila = una muestra. Se ordena por `inicio_at` cuando está presente
    para que el split sin shuffle (el de siempre) sea cronológico.
    """
    filas = _leer_filas_dataset(path)
    if not filas:
        return []
    faltantes = [c for c in _CAMPOS_DATASET if c not in filas[0]]
    if faltantes:
        raise ValueError(
            f"{path}: faltan columnas del dataset: {', '.join(faltantes)}"
        )

    salida: list[tuple[datetime | None, MuestraLote]] = []
    for fila in filas:
        numeros = [_numero(fila.get(c)) for c in _CAMPOS_DATASET[1:]]
        if any(v is None for v in numeros):
            logger.warning("fila descartada (valor no numérico): %s", fila)
            continue
        temp, vel, crudos, ok, quemados, tmin, tmax, vmin, vmax = numeros
        salida.append(
            (
                _inicio_at(fila.get("inicio_at")),
                MuestraLote(
                    producto_id=str(fila["producto_id"]),
                    temp_horno_1=temp,
                    velocidad_cinta=vel,
                    temp_comb_1=_numero(fila.get("temp_comb_1")),
                    humedad_horno=_numero(fila.get("humedad_horno")),
                    pct_crudos=crudos,
                    pct_ok=ok,
                    pct_quemados=quemados,
                    temp_min=tmin,
                    temp_max=tmax,
                    vel_min=vmin,
                    vel_max=vmax,
                ),
            )
        )

    if salida and all(inicio is not None for inicio, _ in salida):
        salida.sort(key=lambda par: par[0])
    return [muestra for _, muestra in salida]


def agrupar_por_producto(muestras: list[MuestraLote]) -> dict[str, list[MuestraLote]]:
    out: dict[str, list[MuestraLote]] = defaultdict(list)
    for m in muestras:
        out[m.producto_id].append(m)
    return out


def _split_train_test(
    items: list[MuestraLote], test_size: float
) -> tuple[list[MuestraLote], list[MuestraLote]]:
    """Split simple sin shuffle (con `--dataset` el orden es cronológico)."""
    if not items:
        return [], []
    corte = max(1, int(len(items) * (1 - test_size)))
    return items[:corte], items[corte:]


def _mae(y_true: list[float], y_pred: list[float]) -> float:
    if not y_true:
        return 0.0
    return sum(abs(a - b) for a, b in zip(y_true, y_pred)) / len(y_true)


def entrenar_producto(
    muestras: list[MuestraLote],
    min_muestras: int,
    test_size: float,
) -> int | None:
    """Entrena (o rechaza) un producto. Devuelve la versión nueva o None."""
    if xgb is None:
        logger.warning("xgboost no instalado, saltando entrenamiento")
        return None
    if len(muestras) < min_muestras:
        logger.info(
            "producto %s: sólo %d muestras (< %d), no se entrena",
            muestras[0].producto_id if muestras else "?",
            len(muestras),
            min_muestras,
        )
        return None

    train, test = _split_train_test(muestras, test_size)
    if len(train) < max(5, min_muestras // 2):
        logger.info("producto %s: split train demasiado chico", train[0].producto_id)
        return None

    X_train = [_features_de_muestra(m) for m in train]
    y_train_crudos = [_targets(m)[0] for m in train]
    y_train_ok = [_targets(m)[1] for m in train]
    y_train_quemados = [_targets(m)[2] for m in train]

    booster_c = _entrenar_uno(X_train, y_train_crudos, "crudos")
    booster_o = _entrenar_uno(X_train, y_train_ok, "ok")
    booster_q = _entrenar_uno(X_train, y_train_quemados, "quemados")

    # Métrica de salida: MAE por cabeza sobre test.
    if test:
        X_test = [_features_de_muestra(m) for m in test]
        dmat_test = xgb.DMatrix(X_test)
        mae_c = _mae([_targets(m)[0] for m in test], booster_c.predict(dmat_test))
        mae_o = _mae([_targets(m)[1] for m in test], booster_o.predict(dmat_test))
        mae_q = _mae([_targets(m)[2] for m in test], booster_q.predict(dmat_test))
        logger.info(
            "producto %s: MAE crudos=%.2f ok=%.2f quemados=%.2f",
            muestras[0].producto_id, mae_c, mae_o, mae_q,
        )

    producto_id = muestras[0].producto_id
    nueva_version = _version_actual(producto_id) + 1
    base = _directorio_modelos()
    base.mkdir(parents=True, exist_ok=True)
    out = base / f"producto_{producto_id}_v{nueva_version}.json"
    out.write_text(
        json.dumps(
            {
                "version": f"v{nueva_version}",
                "crudos": booster_c.save_raw().decode("utf-8"),
                "ok": booster_o.save_raw().decode("utf-8"),
                "quemados": booster_q.save_raw().decode("utf-8"),
            },
            ensure_ascii=False,
        ),
        encoding="utf-8",
    )
    logger.info("producto %s: guardado %s", producto_id, out.name)
    return int(nueva_version)


def _entrenar_uno(X: list[list[float]], y: list[float], label: str) -> "xgb.Booster":
    dtrain = xgb.DMatrix(X, label=y)
    params = {
        "objective": "reg:squarederror",
        "max_depth": 4,
        "eta": 0.1,
        "subsample": 0.8,
        "colsample_bytree": 0.8,
        "min_child_weight": 3,
        "verbosity": 0,
    }
    booster = xgb.train(params, dtrain, num_boost_round=80)
    logger.info("entrenado booster %s: %d muestras", label, len(X))
    return booster


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Entrena XGBoost per-producto.")
    parser.add_argument("--producto", help="Entrenar sólo este producto (UUID)")
    parser.add_argument(
        "--min-muestras", type=int, default=DEFAULT_MIN_MUESTRAS,
        help="Mínimo de muestras para entrenar (default 30)",
    )
    parser.add_argument(
        "--test-size", type=float, default=DEFAULT_TEST_SIZE,
        help="Proporción de test (default 0.2)",
    )
    parser.add_argument(
        "--dataset",
        help="Entrenar desde ventanas exportadas (csv o jsonl) en vez de la BD",
    )
    args = parser.parse_args(argv)

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    if args.dataset:
        muestras = cargar_muestras_dataset(Path(args.dataset))
    else:
        db_url = os.environ.get("DATABASE_URL")
        if not db_url:
            logger.error("DATABASE_URL no está seteada")
            return 1
        muestras = cargar_muestras(db_url)
    if not muestras:
        logger.warning("sin muestras: nada que entrenar")
        return 0
    por_producto = agrupar_por_producto(muestras)
    if args.producto:
        por_producto = {
            args.producto: por_producto.get(args.producto, []),
        }

    entrenados = 0
    for _, items in por_producto.items():
        v = entrenar_producto(items, args.min_muestras, args.test_size)
        if v is not None:
            entrenados += 1
    logger.info("entrenamiento batch OK: %d modelos", entrenados)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))