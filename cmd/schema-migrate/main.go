package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load(".env")
	common.InitEnv()
	if strings.TrimSpace(os.Getenv("SQL_DSN")) == "" {
		fmt.Fprintln(os.Stderr, "SQL_DSN is required for schema migration; use SQL_DSN=local only for an intentional SQLite migration")
		os.Exit(1)
	}
	// The standalone command migrates only the OAuth schema required by this
	// release. It does not start HTTP, Redis, schedulers, background workers, or
	// unrelated migrations.
	if err := model.InitOAuthSchemaMigration(); err != nil {
		fmt.Fprintln(os.Stderr, "schema migration failed:", err)
		os.Exit(1)
	}
	if model.DB != nil {
		sqlDB, err := model.DB.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	}
	fmt.Println("schema migration completed")
}
