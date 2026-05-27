# Smart-Check Automation Backend 🚀

Este es el backend transaccional de la aplicación para **Fermar**, diseñado bajo una **Arquitectura de Capas / Clean Architecture** en **Golang**. El sistema se encarga de monitorear el estado de los hornos industriales, disparar alertas transaccionales ante anomalías y simular análisis de visión artificial mediante YOLO en la cinta transportadora de carga.

---

## 🏗️ Estructura del Proyecto
El código se organiza siguiendo principios de diseño limpio y dominio desacoplado:
*   `cmd/server/`: Punto de entrada (`main.go`) que inicializa las capas e inyecta dependencias.
*   `internal/domain/`: Capa de dominio pura sin dependencias externas. Contiene entidades e interfaces (`horno`, `alerta`).
*   `internal/service/`: Lógica de negocio (procesamiento de umbrales térmicos y disparadores de alertas).
*   `internal/controller/`: Capa de presentación HTTP (manejadores de rutas y validación de entradas).
*   `internal/provider/`: Adaptadores externos de infraestructura (Simulación de base de datos MySQL e integración YOLO).
*   `pkg/`: Paquetes comunes compartidos (utilidades de respuestas JSON estándar).

---

## 🛠️ Requisitos Previos
*   **Go** versión `1.20` o superior instalado. Puedes comprobar tu versión ejecutando:
    ```bash
    go version
    ```

---

## 🚀 Cómo Levantar el Servidor en Local

1.  **Abrir una terminal** en el directorio raíz del proyecto (`test_backend_go/`).
2.  **Ejecutar el comando de arranque**:
    ```bash
    go run cmd/server/main.go
    ```
3.  El servidor iniciará e imprimirá los logs de inicialización de telemetría de Industria 4.0:
    ```text
    Initializing Smart-Check Automation Backend (Layered/Clean Architecture - Modular)...
    Transactional server running on http://localhost:8080
    ```

---

## 🧪 Cómo Ejecutar las Pruebas Unitarias
El proyecto cuenta con cobertura de pruebas automatizadas en todas sus capas críticas. Para correrlas, ejecuta:
```bash
go test ./internal/... -v
```

---

## 📡 Guía de Interacción con los Endpoints

El servidor expone los siguientes endpoints HTTP nativos en el puerto `8080`.

### 1. Endpoint Índice / Estado de Servidor (`GET /`)
Comprueba si el servidor web está corriendo correctamente.

*   **Bash / cURL**:
    ```bash
    curl -i http://localhost:8080/
    ```
*   **Windows PowerShell**:
    ```powershell
    Invoke-RestMethod -Uri "http://localhost:8080/"
    ```

---

### 2. Consulta de Estado de Horno (`GET /api/v1/horno`)
Obtiene el estado en tiempo real del horno. Ejecuta una inspección automática de la cinta mediante visión computacional YOLO. Si detecta un defecto visual (15% de probabilidad simulada), registrará una alerta crítica y cambiará el estado del horno a `MANTENIMIENTO`.

*   **Parámetros query requeridos**:
    *   `id`: Identificador del horno (por defecto el inicializado es `horno-01`).

*   **Bash / cURL**:
    ```bash
    curl -i "http://localhost:8080/api/v1/horno?id=horno-01"
    ```
*   **Windows PowerShell**:
    ```powershell
    Invoke-RestMethod -Uri "http://localhost:8080/api/v1/horno?id=horno-01" | ConvertTo-Json -Depth 10
    ```

*   **Respuesta JSON esperada**:
    ```json
    {
      "success": true,
      "message": "Estado de Horno verificado exitosamente",
      "data": {
        "conveyor_checked": true,
        "horno": {
          "id": "horno-01",
          "nombre": "Horno Rotativo de Clinkerización A-1",
          "temperatura": 185.3,
          "estado": "ACTIVO",
          "ultimo_check": "2026-05-26T22:40:18.7404336-03:00"
        },
        "alertas_recientes": [],
        "saludo": "Hola Mundo desde el controlador de Horno en Arquitectura de Capas Go!"
      }
    }
    ```

---

### 3. Actualizar Temperatura e Historial Transaccional (`POST /api/v1/horno/temperatura`)
Actualiza manualmente la temperatura de un horno. Evalúa las siguientes reglas lógicas del servicio de forma automática:
*   Si temperatura **> 180°C**: Genera una alerta transaccional de tipo `WARNING` y pasa el horno a estado `ATENCION`.
*   Si temperatura **> 200°C**: Genera una alerta transaccional de tipo `CRITICAL` en base de datos y bloquea el horno en estado `MANTENIMIENTO`.
*   Si temperatura **<= 180°C**: El horno opera en rango seguro, el estado es `ACTIVO`.

*   **Esquema del Body JSON**:
    ```json
    {
      "id": "horno-01",
      "temperatura": 212.8
    }
    ```

*   **Bash / cURL**:
    ```bash
    curl -i -X POST \
      -H "Content-Type: application/json" \
      -d '{"id":"horno-01","temperatura":212.8}' \
      http://localhost:8080/api/v1/horno/temperatura
    ```
*   **Windows PowerShell**:
    ```powershell
    Invoke-RestMethod -Uri "http://localhost:8080/api/v1/horno/temperatura" \
      -Method Post \
      -Body '{"id":"horno-01","temperatura":212.8}' \
      -ContentType "application/json" | ConvertTo-Json -Depth 10
    ```

*   **Respuesta JSON esperada (Disparo de Alerta Crítica)**:
    ```json
    {
      "success": true,
      "message": "Temperatura del horno actualizada transaccionalmente",
      "data": {
        "alertas_totales": 1,
        "horno": {
          "id": "horno-01",
          "nombre": "Horno Rotativo de Clinkerización A-1",
          "temperatura": 212.8,
          "estado": "MANTENIMIENTO",
          "ultimo_check": "2026-05-26T22:40:29.4106943-03:00"
        },
        "ultima_alerta": {
          "id": "alt-temp-1779846029410694300",
          "horno_id": "horno-01",
          "nivel": "CRITICAL",
          "mensaje": "Temperatura crítica excedida: 212.8°C. Límite seguro: 200.0°C (Previa: 185.3°C)",
          "creada_en": "2026-05-26T22:40:29.4106943-03:00"
        }
      }
    }
    ```
