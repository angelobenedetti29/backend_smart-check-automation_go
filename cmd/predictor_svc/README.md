# Sidecar predictor XGBoost per-producto (SCA-400)

Servicio HTTP que el backend Go invoca por cada decisión del MPC del horno.
Devuelve `pctCrudos`, `pctOk`, `pctQuemados`, `modelVersion` y
`esPlaceholder` para una configuración dada del horno (T1, velocidad,
combustión, humedad).

La instalación completa del backend + sidecar está en
[`../../MANUAL_INSTALACION.md`](../../MANUAL_INSTALACION.md).

## Por qué per-producto

Cada producto (tostada, pan francés, medialuna...) tiene una dinámica
térmica distinta. Un único modelo global promediaría dinámicas
heterogéneas. Por eso hay un XGBoost por `producto_id`.

## Modos de operación

- **Placeholder** (default al inicio): un modelo determinístico calibrado a
  ojo a partir del setpoint y rangos del producto. No aprende. Existe
  para que el MPC tenga algo con qué optimizar antes de juntar histórico.
- **XGBoost entrenado**: cuando `train.py` corre y junta ≥ N muestras por
  producto (default 30), produce un artefacto JSON
  `models_xgb/producto_{uuid}_v{n}.json` (`save_raw(raw_format="json")`).
  El sidecar lo carga en el próximo `/predict`.

## Endpoints

- `GET /health` — estado del sidecar (incluye lista de productos con modelo).
- `POST /predict` — body (el request acepta `snake_case` y sus alias
  `camelCase`):
  ```json
  {
    "producto_id": "uuid",
    "temp_horno_1": 195.0,
    "velocidad_cinta": 0.22,
    "temp_comb_1": 800.0,
    "humedad_horno": 12.0,
    "temp_min": 180.0,
    "temp_max": 220.0,
    "velocidad_cinta_min": 0.10,
    "velocidad_cinta_max": 0.30
  }
  ```
  Respuesta (JSON en `camelCase`, igual que `optimizador.PrediccionSalida`):
  ```json
  {
    "pctCrudos": 2.1,
    "pctOk": 95.4,
    "pctQuemados": 2.5,
    "modelVersion": "xgb_v3",
    "esPlaceholder": false
  }
  ```
- `POST /admin/recargar/{producto_id}` — invalida cache para recargar modelo.
- `GET /admin/modelos` — lista de IDs con modelo entrenado.

## Entrenamiento

Hay dos caminos. El histórico completo (BD) agrega **una muestra por lote**
(promedio de la telemetría y etiquetas de todos los eventos del lote). El
export por ventanas replica lo que el MPC ve en inferencia: una muestra
punto-en-el-tiempo por ventana, con features "as of" el fin de la ventana y
etiquetas de los eventos de esa ventana. El corte `--split-temporal` es por
lote: todas las ventanas de un lote caen del mismo lado para no filtrar datos.

```bash
# 1) Exportar lotes cerrados a ventanas (CSV o JSONL + manifest.json).
DATABASE_URL='postgres://user:pass@host/db' \
python -m cmd.predictor_svc.export_dataset --out data/windows --split-temporal 0.8

# 2) Entrenar con las ventanas exportadas (sin DATABASE_URL).
python -m cmd.predictor_svc.train --dataset data/windows/train.csv
python -m cmd.predictor_svc.train --dataset data/windows/train.csv --producto <uuid>

# Dataset sintético incluido para demos/pruebas (6 productos, 240 ventanas c/u).
# No reemplaza datos reales: ejercita el pipeline y el MPC.
python -m cmd.predictor_svc.train --dataset cmd/predictor_svc/data/sintetico_6_productos.csv

# O el camino histórico de siempre (una muestra por lote):
DATABASE_URL='postgres://user:pass@host/db' \
python -m cmd.predictor_svc.train
# o para un solo producto:
DATABASE_URL='...' python -m cmd.predictor_svc.train --producto <uuid>
```

`export_dataset.py` documenta la política de ventanas y etiquetas en su
docstring (ventanas solapadas, `estado IS NULL` fuera del denominador).
El script entrena por producto si hay ≥ N muestras (default 30). Guarda
en `models_xgb/`. El próximo `/predict` levanta el modelo nuevo.

## Tests

`pytest` **no** está en `requirements.txt`; instalarlo aparte y correr los tests
desde la raíz del repositorio:

```bash
pip install pytest
python -m pytest cmd/predictor_svc/tests
```

`pytest.ini` configura `--import-mode=importlib` para evitar la colisión del
paquete `cmd/` con el módulo estándar `cmd` de Python.

## Run local

```bash
pip install -r cmd/predictor_svc/requirements.txt
uvicorn cmd.predictor_svc.main:app --reload --port 8090
```

## Run docker

```bash
docker build -f cmd/predictor_svc/Dockerfile -t predictor-svc .
docker run -p 8090:8090 predictor-svc
```