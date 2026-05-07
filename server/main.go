package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	authv2connect "github.com/harvesthub-gardening-tool/protos-go/auth/v2/authv2connect"
	chatv1connect "github.com/harvesthub-gardening-tool/protos-go/chat/v1/chatv1connect"
	controlv1connect "github.com/harvesthub-gardening-tool/protos-go/control/v1/controlv1connect"
	"github.com/harvesthub-gardening-tool/protos-go/garden/v2/gardenv2connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

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

	gormDB, err := gorm.Open(postgres.Open(dbURL), &gorm.Config{
		Logger: logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             time.Second,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  false,
			},
		),
	})
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
		&auth.MotorCommand{},
		&auth.MotorCommandEvent{},
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
	authSvcV2 := service.NewAuthServiceV2(authService)
	controlSvc := service.NewControlService(gormDB)
	chatSvc := service.NewChatService(gormDB, service.ChatServiceConfig{
		APIKey:       os.Getenv("MISTRAL_API_KEY"),
		AgentID:      os.Getenv("MISTRAL_AGENT_ID"),
		AgentVersion: parseEnvInt("MISTRAL_AGENT_VERSION", 1),
		BaseURL:      os.Getenv("MISTRAL_BASE_URL"),
	})

	// Create mux and register services
	mux := http.NewServeMux()

	// Register GardenService v2 with authentication
	gardenPath, gardenHandler := gardenv2connect.NewGardenServiceHandler(
		gardenSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(gardenPath, gardenHandler)

	// Register AuthService v2 — interceptor skips auth for Register/Login/ClaimHubToken internally
	authV2Path, authV2Handler := authv2connect.NewAuthServiceHandler(
		authSvcV2,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(authV2Path, authV2Handler)

	controlPath, controlHandler := controlv1connect.NewControlServiceHandler(
		controlSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(controlPath, controlHandler)

	chatPath, chatHandler := chatv1connect.NewChatServiceHandler(
		chatSvc,
		connect.WithInterceptors(authInterceptor),
	)
	mux.Handle(chatPath, chatHandler)

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

func parseEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscan(value, &parsed); err != nil {
		return fallback
	}
	return parsed
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
