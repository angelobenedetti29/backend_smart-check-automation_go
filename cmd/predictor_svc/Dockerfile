# Sidecar predictor XGBoost per-producto (SCA-400).
FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app

COPY cmd/predictor-svc/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY cmd/predictor-svc/ /app/

# Directorio donde se persisten los modelos entrenados. Se monta como volumen
# para sobrevivir reinicios del contenedor.
RUN mkdir -p /app/models_xgb
VOLUME ["/app/models_xgb"]

EXPOSE 8090

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8090"]