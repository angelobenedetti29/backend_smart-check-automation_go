"""Exporta el histórico a muestras por ventana, punto-en-el-tiempo (SCA-400).

`train.py` agrega una muestra por lote entero (AVG de toda la telemetría y
etiquetas de todos los eventos), pero el MPC en inferencia manda una muestra
punto-en-el-tiempo y las alertas se calculan por ventana. Este script replica
ese esquema: recorre cada lote cerrado ventana por ventana y exporta una fila
por ventana con las features "as of" el fin de la ventana y las etiquetas
observadas dentro de la ventana.

Ventanas
--------
Arrancan en `inicio_at` del lote. Largo = `ventana_evaluacion_segundos`
(default 600 si NULL/<=0), paso = `ventana_actualizacion_segundos` (default
300). Si el paso es menor que el largo las ventanas SE SOLAPAN; es esperado:
cada muestra es lo que el sistema veía al momento de decidir. La última
ventana se recorta en `fin_at` (nunca se extiende fuera del lote).

Features
--------
Se toma la última fila de `metricas_horno` con `received_at <= ventana_fin`.
Si no hay ninguna, o `temp_horno_1`/`velocidad_cinta` son NULL, la ventana se
descarta.

Etiquetas
---------
Cuentan los `eventos_lote` con `momento` en `[ventana_inicio, ventana_fin)` y
`estado IN ('ok','crudo','quemado')`. Los eventos con `estado IS NULL` se
excluyen del numerador Y del denominador; los eventos sin `momento` no se
pueden ubicar en el tiempo y también se excluyen. `pct_*` van en 0..100 y
`confianza_promedio` es AVG(confianza) de esos mismos eventos (NULL si
ninguno trae confianza). Si el denominador < `--min-eventos`, la ventana se
descarta.

Uso:
    DATABASE_URL=postgres://... python -m cmd.predictor_svc.export_dataset \\
        --out data/windows --split-temporal 0.8

Luego:
    python -m cmd.predictor_svc.train --dataset data/windows/train.csv
"""

from __future__ import annotations

import argparse
import csv
import json
import logging
import os
import sys
from collections import Counter
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Iterable, Sequence

try:
    import psycopg2  # type: ignore
    import psycopg2.extras  # type: ignore
except ImportError:
    psycopg2 = None

logger = logging.getLogger(__name__)

SCHEMA_VERSION = 1
DEFAULT_VENTANA_EVALUACION_SEGUNDOS = 600
DEFAULT_VENTANA_ACTUALIZACION_SEGUNDOS = 300
DEFAULT_MIN_EVENTOS = 5
ESTADOS_VALIDOS = ("ok", "crudo", "quemado")

COLUMNAS = [
    "producto_id",
    "lote_id",
    "inicio_at",
    "ventana_inicio",
    "ventana_fin",
    "temp_horno_1",
    "velocidad_cinta",
    "temp_comb_1",
    "humedad_horno",
    "temp_min",
    "temp_max",
    "vel_min",
    "vel_max",
    "pct_crudos",
    "pct_ok",
    "pct_quemados",
    "n_eventos",
    "confianza_promedio",
]

POLITICA_ETIQUETAS = (
    "Ventana [ventana_inicio, ventana_fin). Cuentan eventos con momento en el "
    "rango y estado IN ('ok','crudo','quemado'); estado NULL se excluye del "
    "numerador y del denominador, y los eventos sin momento no se ubican. "
    "pct_* en 0..100; confianza_promedio = AVG(confianza) de esos eventos."
)


@dataclass(frozen=True, slots=True)
class Lote:
    id: str
    producto_id: str
    inicio_at: datetime
    fin_at: datetime
    ventana_evaluacion_segundos: int | None
    ventana_actualizacion_segundos: int | None
    temp_min: float
    temp_max: float
    vel_min: float
    vel_max: float


@dataclass(frozen=True, slots=True)
class Metrica:
    received_at: datetime
    temp_horno_1: float | None
    velocidad_cinta: float | None
    temp_comb_1: float | None
    humedad_horno: float | None


@dataclass(frozen=True, slots=True)
class Evento:
    momento: datetime
    estado: str | None
    confianza: float | None


# ---------------------------------------------------------------------------
# Lógica pura (testeable sin DB)
# ---------------------------------------------------------------------------


def generar_ventanas(
    inicio_at: datetime,
    fin_at: datetime,
    evaluacion_segundos: int | None,
    actualizacion_segundos: int | None,
) -> list[tuple[datetime, datetime]]:
    """Ventanas [inicio, fin) solapadas del lote, recortadas en `fin_at`."""
    largo = (
        evaluacion_segundos
        if evaluacion_segundos and evaluacion_segundos > 0
        else DEFAULT_VENTANA_EVALUACION_SEGUNDOS
    )
    paso = (
        actualizacion_segundos
        if actualizacion_segundos and actualizacion_segundos > 0
        else DEFAULT_VENTANA_ACTUALIZACION_SEGUNDOS
    )
    if fin_at <= inicio_at:
        return []
    ventanas: list[tuple[datetime, datetime]] = []
    cursor = inicio_at
    while cursor < fin_at:
        fin = min(cursor + timedelta(seconds=largo), fin_at)
        ventanas.append((cursor, fin))
        cursor += timedelta(seconds=paso)
    return ventanas


def metrica_as_of(metricas: Sequence[Metrica], cuando: datetime) -> Metrica | None:
    """Última métrica con received_at <= cuando (lista ordenada por tiempo)."""
    elegida = None
    for m in metricas:
        if m.received_at > cuando:
            break
        elegida = m
    return elegida


def agregar_etiquetas(
    eventos: Iterable[Evento],
    ventana_inicio: datetime,
    ventana_fin: datetime,
) -> dict[str, Any] | None:
    """Agrega los eventos de la ventana. None si no hay ninguno válido."""
    en_ventana = [
        e
        for e in eventos
        if ventana_inicio <= e.momento < ventana_fin and e.estado in ESTADOS_VALIDOS
    ]
    n = len(en_ventana)
    if n == 0:
        return None
    cuentas = Counter(e.estado for e in en_ventana)
    confianzas = [e.confianza for e in en_ventana if e.confianza is not None]
    return {
        "pct_crudos": 100.0 * cuentas["crudo"] / n,
        "pct_ok": 100.0 * cuentas["ok"] / n,
        "pct_quemados": 100.0 * cuentas["quemado"] / n,
        "n_eventos": n,
        "confianza_promedio": (
            sum(confianzas) / len(confianzas) if confianzas else None
        ),
    }


def construir_filas_lote(
    lote: Lote,
    metricas: Sequence[Metrica],
    eventos: Sequence[Evento],
    min_eventos: int = DEFAULT_MIN_EVENTOS,
) -> list[dict[str, Any]]:
    """Replica el lote en filas por ventana, con features y etiquetas."""
    filas: list[dict[str, Any]] = []
    for ventana_inicio, ventana_fin in generar_ventanas(
        lote.inicio_at,
        lote.fin_at,
        lote.ventana_evaluacion_segundos,
        lote.ventana_actualizacion_segundos,
    ):
        metrica = metrica_as_of(metricas, ventana_fin)
        if (
            metrica is None
            or metrica.temp_horno_1 is None
            or metrica.velocidad_cinta is None
        ):
            continue
        etiquetas = agregar_etiquetas(eventos, ventana_inicio, ventana_fin)
        if etiquetas is None or etiquetas["n_eventos"] < min_eventos:
            continue
        filas.append(
            {
                "producto_id": lote.producto_id,
                "lote_id": lote.id,
                "inicio_at": lote.inicio_at,
                "ventana_inicio": ventana_inicio,
                "ventana_fin": ventana_fin,
                "temp_horno_1": metrica.temp_horno_1,
                "velocidad_cinta": metrica.velocidad_cinta,
                "temp_comb_1": metrica.temp_comb_1,
                "humedad_horno": metrica.humedad_horno,
                "temp_min": lote.temp_min,
                "temp_max": lote.temp_max,
                "vel_min": lote.vel_min,
                "vel_max": lote.vel_max,
                **etiquetas,
            }
        )
    return filas


def construir_manifest(
    filas: Sequence[dict[str, Any]],
    lotes: Sequence[Lote],
    *,
    formato: str,
    min_eventos: int,
    generado_en: datetime | None = None,
) -> dict[str, Any]:
    """Metadata del export para que el entrenamiento sepa con qué se generó."""
    configs = {
        (
            l.ventana_evaluacion_segundos or DEFAULT_VENTANA_EVALUACION_SEGUNDOS,
            l.ventana_actualizacion_segundos or DEFAULT_VENTANA_ACTUALIZACION_SEGUNDOS,
        )
        for l in lotes
    }
    # shortcut: el manifest resume una sola config de ventanas; si los lotes
    # difieren se reportan los defaults (la config real vive por lote en la BD).
    evaluacion, actualizacion = (
        next(iter(configs))
        if len(configs) == 1
        else (
            DEFAULT_VENTANA_EVALUACION_SEGUNDOS,
            DEFAULT_VENTANA_ACTUALIZACION_SEGUNDOS,
        )
    )
    return {
        "generado_en": (generado_en or datetime.now(timezone.utc)).isoformat(),
        "ventana_evaluacion_segundos": evaluacion,
        "ventana_actualizacion_segundos": actualizacion,
        "min_eventos": min_eventos,
        "formato": formato,
        "filas": len(filas),
        "lotes": len({f["lote_id"] for f in filas}),
        "productos": dict(Counter(f["producto_id"] for f in filas)),
        "politica_etiquetas": POLITICA_ETIQUETAS,
        "schema_version": SCHEMA_VERSION,
    }


def _serializar(fila: dict[str, Any]) -> dict[str, Any]:
    return {
        k: v.isoformat() if isinstance(v, datetime) else v
        for k, v in fila.items()
    }


def _escribir(filas: Sequence[dict[str, Any]], path: Path, formato: str) -> None:
    with path.open("w", encoding="utf-8", newline="") as fh:
        if formato == "csv":
            writer = csv.DictWriter(fh, fieldnames=COLUMNAS, extrasaction="ignore")
            writer.writeheader()
            for fila in filas:
                writer.writerow(_serializar(fila))
        else:
            for fila in filas:
                fh.write(json.dumps(_serializar(fila), ensure_ascii=False) + "\n")


def escribir_dataset(
    filas: Sequence[dict[str, Any]],
    out_dir: Path,
    formato: str,
    split_temporal: float | None,
) -> list[Path]:
    """Escribe dataset.<ext> o train/val.<ext> (corte cronológico por inicio_at)."""
    out_dir.mkdir(parents=True, exist_ok=True)
    ext = "csv" if formato == "csv" else "jsonl"
    ordenadas = sorted(filas, key=lambda f: f["inicio_at"])
    if split_temporal is None:
        partes = [("dataset", ordenadas)]
    else:
        # Corte por lote, no por fila: todas las ventanas de un lote comparten
        # inicio_at y no deben repartirse entre train y val (fuga de datos).
        lotes = list(dict.fromkeys(f["lote_id"] for f in ordenadas))
        corte = int(len(lotes) * split_temporal)
        if lotes:
            corte = min(max(1, corte), len(lotes))
        en_train = set(lotes[:corte])
        partes = [
            ("train", [f for f in ordenadas if f["lote_id"] in en_train]),
            ("val", [f for f in ordenadas if f["lote_id"] not in en_train]),
        ]
    paths: list[Path] = []
    for nombre, subset in partes:
        path = out_dir / f"{nombre}.{ext}"
        _escribir(subset, path, formato)
        paths.append(path)
    return paths


# ---------------------------------------------------------------------------
# Carga desde la BD
# ---------------------------------------------------------------------------

SQL_LOTES = """
SELECT
  l.id, l.producto_id, l.inicio_at, l.fin_at,
  l.ventana_evaluacion_segundos, l.ventana_actualizacion_segundos,
  p.temp_min, p.temp_max, p.velocidad_cinta_min, p.velocidad_cinta_max
FROM lotes_productivos l
JOIN productos p ON p.id = l.producto_id
WHERE l.estado = 'CERRADO'
  AND l.fin_at IS NOT NULL
  {filtro}
ORDER BY l.inicio_at, l.id
"""

SQL_METRICAS = """
SELECT lote_id, received_at, temp_horno_1, velocidad_cinta,
       temp_comb_1, humedad_horno
FROM metricas_horno
WHERE lote_id IN %s
ORDER BY lote_id, received_at
"""

SQL_EVENTOS = """
SELECT lote_id, momento, estado, confianza
FROM eventos_lote
WHERE lote_id IN %s AND momento IS NOT NULL
ORDER BY lote_id, momento
"""


def _requerir_psycopg2() -> None:
    if psycopg2 is None:
        raise RuntimeError(
            "psycopg2 no está instalado: pip install psycopg2-binary"
        )


def cargar_historico(
    database_url: str, producto_id: str | None = None
) -> tuple[list[Lote], dict[str, list[Metrica]], dict[str, list[Evento]]]:
    """Lotes cerrados + telemetría y eventos por lote."""
    _requerir_psycopg2()
    filtro = "AND l.producto_id = %s" if producto_id else ""
    params: tuple[Any, ...] = (producto_id,) if producto_id else ()

    conn = psycopg2.connect(database_url)
    try:
        cur = conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
        cur.execute(SQL_LOTES.format(filtro=filtro), params)
        lotes: list[Lote] = []
        for row in cur.fetchall():
            if None in (
                row["temp_min"],
                row["temp_max"],
                row["velocidad_cinta_min"],
                row["velocidad_cinta_max"],
            ):
                logger.warning(
                    "lote %s sin rangos de producto: se omite", row["id"]
                )
                continue
            lotes.append(
                Lote(
                    id=str(row["id"]),
                    producto_id=str(row["producto_id"]),
                    inicio_at=row["inicio_at"],
                    fin_at=row["fin_at"],
                    ventana_evaluacion_segundos=row["ventana_evaluacion_segundos"],
                    ventana_actualizacion_segundos=row[
                        "ventana_actualizacion_segundos"
                    ],
                    temp_min=float(row["temp_min"]),
                    temp_max=float(row["temp_max"]),
                    vel_min=float(row["velocidad_cinta_min"]),
                    vel_max=float(row["velocidad_cinta_max"]),
                )
            )
        if not lotes:
            return [], {}, {}

        ids = tuple(l.id for l in lotes)
        cur.execute(SQL_METRICAS, (ids,))
        metricas: dict[str, list[Metrica]] = {}
        for row in cur.fetchall():
            metricas.setdefault(str(row["lote_id"]), []).append(
                Metrica(
                    received_at=row["received_at"],
                    temp_horno_1=_opt_float(row["temp_horno_1"]),
                    velocidad_cinta=_opt_float(row["velocidad_cinta"]),
                    temp_comb_1=_opt_float(row["temp_comb_1"]),
                    humedad_horno=_opt_float(row["humedad_horno"]),
                )
            )

        cur.execute(SQL_EVENTOS, (ids,))
        eventos: dict[str, list[Evento]] = {}
        for row in cur.fetchall():
            eventos.setdefault(str(row["lote_id"]), []).append(
                Evento(
                    momento=row["momento"],
                    estado=row["estado"],
                    confianza=_opt_float(row["confianza"]),
                )
            )
    finally:
        conn.close()
    return lotes, metricas, eventos


def _opt_float(x: object) -> float | None:
    return None if x is None else float(x)


def exportar(
    lotes: Sequence[Lote],
    metricas: dict[str, list[Metrica]],
    eventos: dict[str, list[Evento]],
    *,
    out_dir: Path,
    formato: str,
    min_eventos: int,
    split_temporal: float | None,
) -> dict[str, Any]:
    """Construye filas, escribe archivos + manifest y devuelve el manifest."""
    filas: list[dict[str, Any]] = []
    for lote in lotes:
        filas.extend(
            construir_filas_lote(
                lote,
                metricas.get(lote.id, []),
                eventos.get(lote.id, []),
                min_eventos,
            )
        )
    escribir_dataset(filas, out_dir, formato, split_temporal)
    manifest = construir_manifest(
        filas, lotes, formato=formato, min_eventos=min_eventos
    )
    (out_dir / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    return manifest


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Exporta lotes cerrados a muestras por ventana."
    )
    parser.add_argument("--out", required=True, help="Directorio de salida")
    parser.add_argument("--producto", help="Sólo este producto (UUID)")
    parser.add_argument(
        "--formato",
        choices=("csv", "jsonl"),
        default="csv",
        help="Formato de salida (default csv)",
    )
    parser.add_argument(
        "--min-eventos",
        type=int,
        default=DEFAULT_MIN_EVENTOS,
        help="Mínimo de eventos para aceptar una ventana (default 5)",
    )
    parser.add_argument(
        "--split-temporal",
        type=float,
        default=None,
        help="Fracción de train sobre el orden cronológico (ej. 0.8)",
    )
    args = parser.parse_args(argv)

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    if args.split_temporal is not None and not 0 < args.split_temporal <= 1:
        logger.error("--split-temporal debe estar en (0, 1]")
        return 1
    if psycopg2 is None:
        logger.error("psycopg2 no está instalado: pip install psycopg2-binary")
        return 1
    db_url = os.environ.get("DATABASE_URL")
    if not db_url:
        logger.error("DATABASE_URL no está seteada")
        return 1

    lotes, metricas, eventos = cargar_historico(db_url, args.producto)
    manifest = exportar(
        lotes,
        metricas,
        eventos,
        out_dir=Path(args.out),
        formato=args.formato,
        min_eventos=args.min_eventos,
        split_temporal=args.split_temporal,
    )
    logger.info(
        "export OK: %d filas de %d lotes en %s",
        manifest["filas"],
        manifest["lotes"],
        args.out,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
