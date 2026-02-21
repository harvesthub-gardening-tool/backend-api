package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"connectrpc.com/connect"
	authv1connect "github.com/harvesthub-gardening-tool/protos-go/auth/v1/authv1connect"
	"github.com/harvesthub-gardening-tool/protos-go/garden/v1/gardenv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"harvest-hub/api/internal/auth"
	authjwt "harvest-hub/api/internal/auth/jwt"
	"harvest-hub/api/internal/service"
)

func main() {
	// Database connection (single GORM handle for all services)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@db:5432/garden_db?sslmode=disable"
	}

	gormDB, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Auto-migrate all domain tables.
	// Order matters: User first (HubToken, Hub reference it), Hub before SensorNode.
	if err := gormDB.AutoMigrate(
		&auth.User{},
		&auth.HubToken{},
		&auth.Hub{},
		&auth.SensorNode{},
	); err != nil {
		log.Fatalf("Failed to auto-migrate: %v", err)
	}

	// Initialize JWT Manager (RSA 2048-bit keys with persistent storage)
	// Keys are stored in .jwt_private.pem and .jwt_public.pem to prevent
	// token invalidation on server restart (critical for 1-year hub tokens)
	jwtKeyPath := os.Getenv("JWT_KEY_PATH")
	if jwtKeyPath == "" {
		jwtKeyPath = "."
	}
	jwtManager, err := authjwt.NewOrLoadJWTManager(jwtKeyPath)
	if err != nil {
		log.Fatalf("Failed to initialize JWT manager: %v", err)
	}

	// Initialize Auth Service (user registration and hub token management)
	authService := auth.NewAuthService(gormDB, jwtManager)

	// Create JWT auth interceptor (validates JWT tokens)
	authInterceptor := auth.NewJWTAuthInterceptor(jwtManager)

	// Create services (both share the same GORM handle)
	gardenSvc := service.NewGardenService(gormDB)
	authSvc := service.NewAuthService(authService)

	// Create mux and register services
	mux := http.NewServeMux()

	// Register GardenService with authentication
	gardenPath, gardenHandler := gardenv1connect.NewGardenServiceHandler(
		gardenSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(gardenPath, gardenHandler)

	// Register AuthService — interceptor skips auth for Register/Login internally
	authPath, authHandler := authv1connect.NewAuthServiceHandler(
		authSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(authPath, authHandler)

	// Add CORS middleware
	corsHandler := cors(mux)

	// Start server
	addr := ":8080"
	fmt.Printf("Garden API listening on %s\n", addr)
	fmt.Printf("Authentication: JWT with RSA-256\n")
	fmt.Printf("  User tokens: 24h expiry\n")
	fmt.Printf("  Hub tokens: 1y expiry\n")

	if err := http.ListenAndServe(addr, h2c.NewHandler(corsHandler, &http2.Server{})); err != nil {
		log.Fatalf("Server failed: %v", err)
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
