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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"harvest-hub/api/internal/auth"
	authjwt "harvest-hub/api/internal/auth/jwt"
	"harvest-hub/api/internal/service"
)

func main() {
	ctx := context.Background()

	// Database connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@db:5432/garden_db?sslmode=disable"
	}

	// pgxpool for GardenService (TimescaleDB queries)
	pgxDB, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer pgxDB.Close()

	// GORM for AuthService (user/token management)
	gormDB, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect GORM: %v", err)
	}

	// Auto-migrate auth tables
	if err := gormDB.AutoMigrate(&auth.User{}, &auth.HubToken{}); err != nil {
		log.Fatalf("Failed to migrate auth tables: %v", err)
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

	// Create services
	gardenSvc := service.NewGardenService(pgxDB)
	authSvc := service.NewAuthService(authService)

	// Create mux and register services
	mux := http.NewServeMux()

	// Register GardenService with authentication
	gardenPath, gardenHandler := gardenv1connect.NewGardenServiceHandler(
		gardenSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(gardenPath, gardenHandler)

	// Register AuthService endpoints (register/login do not require auth)
	registerAuthEndpoints(mux, authSvc, authInterceptor)

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

// registerAuthEndpoints manually registers AuthService endpoints.
// Register and Login do NOT require authentication.
// CreateHubToken, ListHubTokens, and RevokeHubToken DO require authentication.
func registerAuthEndpoints(mux *http.ServeMux, authSvc *service.AuthService, authInterceptor connect.UnaryInterceptorFunc) {
	// Register and Login - NO auth required
	mux.Handle("/auth.v1.AuthService/Register", connect.NewUnaryHandler(
		"/auth.v1.AuthService/Register",
		authSvc.Register,
	))
	mux.Handle("/auth.v1.AuthService/Login", connect.NewUnaryHandler(
		"/auth.v1.AuthService/Login",
		authSvc.Login,
	))

	// CreateHubToken, ListHubTokens, RevokeHubToken - Auth required
	mux.Handle("/auth.v1.AuthService/CreateHubToken", connect.NewUnaryHandler(
		"/auth.v1.AuthService/CreateHubToken",
		authSvc.CreateHubToken,
		connect.WithInterceptors(authInterceptor),
	))
	mux.Handle("/auth.v1.AuthService/ListHubTokens", connect.NewUnaryHandler(
		"/auth.v1.AuthService/ListHubTokens",
		authSvc.ListHubTokens,
		connect.WithInterceptors(authInterceptor),
	))
	mux.Handle("/auth.v1.AuthService/RevokeHubToken", connect.NewUnaryHandler(
		"/auth.v1.AuthService/RevokeHubToken",
		authSvc.RevokeHubToken,
		connect.WithInterceptors(authInterceptor),
	))
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
