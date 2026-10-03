# Sidecar predictor XGBoost per-producto (SCA-400).
FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app

COPY cmd/predictor_svc/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

# Copiamos el paquete `predictor_svc` (no los archivos sueltos) para que los
# `from .modelo import …` funcionen cuando uvicorn cargue como módulo del
# paquete. Si copiáramos a `/app/` directamente, los imports relativos
# fallarían con `ImportError: attempted relative import with no known parent
# package` porque Python no detecta un paquete padre.
COPY cmd/predictor_svc/ /app/predictor_svc/

# Directorio donde se persisten los modelos entrenados. Vive adentro del
# paquete porque `modelo.py` lo resuelve con `Path(__file__).parent /
# "models_xgb"`. El volumen del docker-compose se llama igual.
RUN mkdir -p /app/predictor_svc/models_xgb
VOLUME ["/app/predictor_svc/models_xgb"]

EXPOSE 8090

CMD ["uvicorn", "predictor_svc.main:app", "--host", "0.0.0.0", "--port", "8090"]