// Comando de migración: aplica schema.sql y las migraciones pendientes.
// Pensado para CI (GitHub Actions) y para operadores que no quieren levantar
// el servidor completo.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	schema "github.com/angelobenedetti29/smart-check-automation/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Advertencia: archivo .env no encontrado, se usarán variables de entorno del sistema")
	}

	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		log.Fatal("DATABASE_URL no configurada. Verificá el archivo .env o la variable de entorno")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := database.NewPostgresPool(ctx, connString)
	if err != nil {
		log.Fatalf("Error al conectar a PostgreSQL: %v", err)
	}
	defer pool.Close()

	if err := schema.Apply(ctx, pool); err != nil {
		log.Fatalf("Error al aplicar schema/migraciones: %v", err)
	}

	log.Println("Schema y migraciones aplicados correctamente.")
}
