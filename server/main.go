package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"github.com/harvesthub-gardening-tool/protos-go/garden/v1/gardenv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"harvest-hub/api/internal/auth"
	"harvest-hub/api/internal/service"
)

func main() {
	ctx := context.Background()

	// Database connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://user:password@db/garden_db?sslmode=disable"
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer db.Close()

	// Zitadel configuration
	zitadelDomain := os.Getenv("ZITADEL_DOMAIN")
	zitadelEnv := "PROD"
	if zitadelDomain == "" {
		// Default for Docker Compose: use host network via host.docker.internal
		// This allows API to reach Zitadel with correct Host header (localhost)
		zitadelDomain = "localhost:8085"
		zitadelEnv = "LOCAL"
	}

	zitadelClientID := os.Getenv("ZITADEL_CLIENT_ID")
	if zitadelClientID == "" {
		log.Fatal("❌ ZITADEL_CLIENT_ID is required")
	}

	// Initialize authentication (Zitadel JWT validation with JWKS caching)
	authCfg := auth.InterceptorConfig{
		HubServiceAccountID: os.Getenv("HUB_SERVICE_ACCOUNT_ID"),
	}
	authInterceptor, err := auth.NewAuthInterceptor(ctx, zitadelDomain, zitadelClientID, zitadelEnv, authCfg)
	if err != nil {
		log.Fatalf("Failed to initialize authentication: %v", err)
	}

	// Create service
	gardenSvc := service.NewGardenService(db)

	// Create mux and register service with Zitadel authentication
	mux := http.NewServeMux()

	path, handler := gardenv1connect.NewGardenServiceHandler(
		gardenSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(path, handler)

	// Add CORS middleware
	corsHandler := cors(mux)

	// Start server
	addr := ":8080"
	fmt.Printf("✅ Garden API listening on %s\n", addr)
	fmt.Printf("🔐 Authentication: Zitadel JWT Validation (JWKS cached)\n")
	fmt.Printf("   - Domain: %s\n", zitadelDomain)
	fmt.Printf("   - Client ID: %s\n", zitadelClientID)
	fmt.Printf("   - Performance: ~1ms validation\n")

	if err := http.ListenAndServe(addr, h2c.NewHandler(corsHandler, &http2.Server{})); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		h.ServeHTTP(w, r)
	})
}
