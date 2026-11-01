# Sidecar predictor XGBoost per-producto (SCA-400)

Servicio HTTP que el backend Go invoca por cada decisión del MPC del horno.
Devuelve `pct_crudos`, `pct_ok`, `pct_quemados` para una configuración
dada del horno (T1, velocidad, combustión, humedad).

## Por qué per-producto

Cada producto (tostada, pan francés, medialuna...) tiene una dinámica
térmica distinta. Un único modelo global promediaría dinámicas
heterogéneas. Por eso hay un XGBoost por `producto_id`.

## Modos de operación

- **Placeholder** (default al inicio): un modelo determinístico calibrado a
  ojo a partir del setpoint y rangos del producto. No aprende. Existe
  para que el MPC tenga algo con qué optimizar antes de juntar histórico.
- **XGBoost entrenado**: cuando `train.py` corre y junta ≥ N muestras por
  producto (default 30), produce un artefacto `producto_{uuid}_v{n}.json`.
  El sidecar lo carga en el próximo `/predict`.

## Endpoints

- `GET /health` — estado del sidecar (incluye lista de productos con modelo).
- `POST /predict` — body:
  ```json
  {
    "producto_id": "uuid",
    "temp_horno_1": 195.0,
    "velocidad_cinta": 0.22,
    "temp_comb_1": 800.0,
    "humedad_horno": 12.0
  }
  ```
  Respuesta:
  ```json
  {
    "pct_crudos": 2.1,
    "pct_ok": 95.4,
    "pct_quemados": 2.5,
    "model_version": "xgb_v3",
    "es_placeholder": false
  }
  ```
- `POST /admin/recargar/{producto_id}` — invalida cache para recargar modelo.
- `GET /admin/modelos` — lista de IDs con modelo entrenado.

## Entrenamiento

```bash
DATABASE_URL='postgres://user:pass@host/db' \
python -m cmd.predictor_svc.train
# o para un solo producto:
DATABASE_URL='...' python -m cmd.predictor_svc.train --producto <uuid>
```

El script entrena por producto si hay ≥ N muestras (default 30). Guarda
en `models_xgb/`. El próximo `/predict` levanta el modelo nuevo.

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