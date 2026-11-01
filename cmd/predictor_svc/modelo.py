"""Modelos XGBoost per-producto con fallback a placeholder (SCA-400).

Cache en memoria (LRU por producto_id). Si no existe un modelo entrenado,
se devuelve el placeholder calibrado por la receta del producto. El
placeholder NO aprende: existe para que el MPC tenga algo con qué
optimizar antes de juntar histórico.

Modelos entrenados se persisten en `models_xgb/producto_{uuid}_v{n}.json`
(formato nativo de XGBoost, portable y diff-friendly). El backend los
identifica por el sufijo `_v{n}` y carga el más reciente.

Para arrancar sin xgboost instalado (modo placeholder puro), el módulo
detecta la ausencia del paquete y degrada a un modelo lineal simple. Esto
permite probar el sidecar antes de instalar ML.
"""

from __future__ import annotations

import json
import logging
import math
import re
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

try:
    import xgboost as xgb  # type: ignore
    _XGBOOST_DISPONIBLE = True
except ImportError:
    _XGBOOST_DISPONIBLE = False

logger = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class Prediccion:
    pct_crudos: float
    pct_ok: float
    pct_quemados: float


@dataclass(frozen=True, slots=True)
class ProductoPlaceholder:
    """Snapshot del catálogo que necesita el placeholder.

    El backend podría enviarlo en cada request o este sidecar podría
    consultarlo. Para v1 lo recibimos en cada request (producto_id) y
    aplicamos defaults razonables. Cuando exista el endpoint de catálogo
    expuesto por el backend Go, este sidecar lo cachea localmente.
    """

    id: str
    nombre: str
    temp_min: float
    temp_max: float
    velocidad_cinta_min: float
    velocidad_cinta_max: float
    temp_setpoint: float | None = None


class ModeloPlaceholder:
    """Modelo determinístico por producto, sin aprendizaje.

    Idea: si la temperatura está por encima del centro del rango, hay más
    quemados y menos crudos. Si está por debajo, al revés. La velocidad
    de cinta alta reduce el tiempo de exposición, similar a baja
    temperatura: más crudos, menos quemados.

    Coeficientes calibrados a ojo; el re-train los reemplaza cuando hay
    histórico suficiente. Mantener la firma estable para que el código
    cliente no cambie.
    """

    name = "placeholder_v1"

    def predict(self, producto: ProductoPlaceholder, req) -> Prediccion:
        centro_t = (
            producto.temp_setpoint
            if producto.temp_setpoint is not None
            else (producto.temp_max + producto.temp_min) / 2.0
        )
        centro_v = (
            (producto.velocidad_cinta_max + producto.velocidad_cinta_min) / 2.0
        )
        # Distancias normalizadas en [-1, 1] aproximadamente.
        ancho_t = max(1.0, producto.temp_max - producto.temp_min)
        ancho_v = max(0.01, producto.velocidad_cinta_max - producto.velocidad_cinta_min)
        d_t = (req.temp_horno_1 - centro_t) / ancho_t
        d_v = (req.velocidad_cinta - centro_v) / ancho_v

        # Quemados crecen con d_t positivo; crudos con d_t negativo.
        pct_quemados = self._sigmoid(8 * (d_t - 0.05)) * 100.0
        pct_crudos = self._sigmoid(-8 * (d_t + 0.05)) * 100.0

        # Velocidad: cinta más rápida = más crudos, menos quemados.
        pct_crudos += self._sigmoid(6 * (d_v - 0.0)) * 5.0
        pct_quemados = max(0.0, pct_quemados - self._sigmoid(6 * (d_v - 0.0)) * 5.0)

        pct_ok = max(0.0, 100.0 - pct_crudos - pct_quemados)
        # Renormalización defensiva por si los offsets la desbalancean.
        total = pct_crudos + pct_ok + pct_quemados
        if total > 0 and abs(total - 100.0) > 0.01:
            pct_crudos = pct_crudos * 100.0 / total
            pct_quemados = pct_quemados * 100.0 / total
            pct_ok = 100.0 - pct_crudos - pct_quemados

        return Prediccion(
            pct_crudos=round(pct_crudos, 2),
            pct_ok=round(pct_ok, 2),
            pct_quemados=round(pct_quemados, 2),
        )

    @staticmethod
    def _sigmoid(x: float) -> float:
        if x > 50:
            return 1.0
        if x < -50:
            return 0.0
        return 1.0 / (1.0 + math.exp(-x))


class ModeloXGB:
    """Wrapper de un XGBoost entrenado por producto.

    Tres árboles en paralelo (uno por cabeza: pct_crudos, pct_ok, pct_quemados).
    La salida se renormaliza para que la suma sea 100.
    """

    def __init__(
        self,
        booster_crudos: "xgb.Booster",
        booster_ok: "xgb.Booster",
        booster_quemados: "xgb.Booster",
        version: str,
    ) -> None:
        self._booster_crudos = booster_crudos
        self._booster_ok = booster_ok
        self._booster_quemados = booster_quemados
        self._version = version

    @property
    def version(self) -> str:
        return self._version

    def predict(self, producto: ProductoPlaceholder, req) -> Prediccion:
        features = _features_de_request(producto, req)
        # XGBoost predice con DMatrix.
        import xgboost as xgb  # type: ignore
        dmat = xgb.DMatrix([features])
        p_c = float(self._booster_crudos.predict(dmat)[0])
        p_o = float(self._booster_ok.predict(dmat)[0])
        p_q = float(self._booster_quemados.predict(dmat)[0])
        # Renormalización a [0, 100] con suma 100.
        total = p_c + p_o + p_q
        if total <= 0:
            return Prediccion(0.0, 100.0, 0.0)
        pct_crudos = max(0.0, p_c * 100.0 / total)
        pct_ok = max(0.0, p_o * 100.0 / total)
        pct_quemados = max(0.0, p_q * 100.0 / total)
        # Re-balanceo para que la suma sea exactamente 100.
        dif = 100.0 - (pct_crudos + pct_ok + pct_quemados)
        pct_ok += dif
        return Prediccion(
            pct_crudos=round(pct_crudos, 2),
            pct_ok=round(pct_ok, 2),
            pct_quemados=round(pct_quemados, 2),
        )


def _features_de_request(producto: ProductoPlaceholder, req) -> list[float]:
    """Vector de features que consume el XGBoost. Orden estable.

    Features:
      - temp_horno_1, velocidad_cinta
      - temp_comb_1 (opcional), humedad_horno (opcional)
      - d_t = (temp_horno_1 - centro_t) / ancho_t
      - d_v = (velocidad_cinta - centro_v) / ancho_v
    """
    centro_t = (
        producto.temp_setpoint
        if producto.temp_setpoint is not None
        else (producto.temp_max + producto.temp_min) / 2.0
    )
    ancho_t = max(1.0, producto.temp_max - producto.temp_min)
    centro_v = (producto.velocidad_cinta_max + producto.velocidad_cinta_min) / 2.0
    ancho_v = max(0.01, producto.velocidad_cinta_max - producto.velocidad_cinta_min)
    d_t = (req.temp_horno_1 - centro_t) / ancho_t
    d_v = (req.velocidad_cinta - centro_v) / ancho_v
    return [
        req.temp_horno_1,
        req.velocidad_cinta,
        req.temp_comb_1 if req.temp_comb_1 is not None else float("nan"),
        req.humedad_horno if req.humedad_horno is not None else float("nan"),
        d_t,
        d_v,
    ]


def _directorio_modelos() -> Path:
    """Resuelve el directorio donde se guardan los modelos entrenados."""
    return Path(__file__).resolve().parent / "models_xgb"


_VERSION_RE = re.compile(r"_v(\d+)\.json$")


def _version_de_archivo(path: Path) -> int:
    m = _VERSION_RE.search(path.name)
    return int(m.group(1)) if m else 0


def cargar_modelo_para_producto(
    producto_id: str,
    placeholder_params: ProductoPlaceholder,
) -> tuple[ModeloXGB | ModeloPlaceholder, str, bool]:
    """Carga el modelo más reciente del producto o devuelve el placeholder.

    Retorna (modelo, model_version, es_placeholder).
    """
    if not _XGBOOST_DISPONIBLE:
        logger.info("predictor: xgboost no instalado, sirviendo placeholder")
        return ModeloPlaceholder(), ModeloPlaceholder.name, True

    base = _directorio_modelos()
    if not base.is_dir():
        return ModeloPlaceholder(), ModeloPlaceholder.name, True

    candidatos = sorted(
        base.glob(f"producto_{producto_id}_v*.json"),
        key=_version_de_archivo,
        reverse=True,
    )
    if not candidatos:
        return ModeloPlaceholder(), ModeloPlaceholder.name, True

    path = candidatos[0]
    try:
        booster_c, booster_o, booster_q = _cargar_xgb_json(path)
    except Exception as exc:  # noqa: BLE001 - aislamos el sidecar
        logger.warning("predictor: no se pudo cargar %s: %s", path, exc)
        return ModeloPlaceholder(), ModeloPlaceholder.name, True

    version = f"xgb_v{_version_de_archivo(path)}"
    logger.info("predictor: cargado %s para producto %s", version, producto_id)
    return ModeloXGB(booster_c, booster_o, booster_q, version), version, False


def _cargar_xgb_json(path: Path) -> tuple:
    """Carga un archivo JSON con tres boosters serializados en uno.

    El formato esperado (ver train.py) es:
      {
        "version": "v1",
        "crudos": "<json de xgb.Booster>",
        "ok": "<json>",
        "quemados": "<json>"
      }
    """
    import xgboost as xgb  # type: ignore
    raw = json.loads(path.read_text(encoding="utf-8"))
    booster_c = xgb.Booster()
    booster_c.load_model(bytearray(raw["crudos"], "utf-8"))
    booster_o = xgb.Booster()
    booster_o.load_model(bytearray(raw["ok"], "utf-8"))
    booster_q = xgb.Booster()
    booster_q.load_model(bytearray(raw["quemados"], "utf-8"))
    return booster_c, booster_o, booster_q


def listar_productos_con_modelo() -> list[str]:
    """Lista los IDs de producto que tienen al menos un modelo entrenado."""
    base = _directorio_modelos()
    if not base.is_dir():
        return []
    ids: set[str] = set()
    for path in base.glob("producto_*_v*.json"):
        m = re.match(r"producto_(.+)_v\d+\.json$", path.name)
        if m:
            ids.add(m.group(1))
    return sorted(ids)


def info_sidecar() -> dict:
    """Snapshot de salud del sidecar (para /health)."""
    return {
        "xgboost_disponible": _XGBOOST_DISPONIBLE,
        "modelos_entrenados": listar_productos_con_modelo(),
        "directorio_modelos": str(_directorio_modelos()),
        "generado_en": datetime.now(timezone.utc).isoformat(),
    }