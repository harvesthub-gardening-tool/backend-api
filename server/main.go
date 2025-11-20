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
	"github.com/zitadel/zitadel-go/v3/pkg/authorization"
	"github.com/zitadel/zitadel-go/v3/pkg/authorization/oauth"
	"github.com/zitadel/zitadel-go/v3/pkg/zitadel"
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
		dbURL = "postgres://user:password@localhost:5432/garden_db?sslmode=disable"
	}

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer db.Close()

	// Zitadel configuration
	zitadelDomain := os.Getenv("ZITADEL_DOMAIN")
	if zitadelDomain == "" {
		zitadelDomain = "localhost:8085"
	}

	zitadelKeyPath := os.Getenv("ZITADEL_KEY_PATH")
	if zitadelKeyPath == "" {
		log.Println("⚠️  ZITADEL_KEY_PATH not set, will try to use introspection without service account")
	}

	// Initialize Zitadel authorization
	var authz *authorization.Authorizer[*oauth.IntrospectionContext]
	if zitadelKeyPath != "" {
		authz, err = authorization.New(ctx, zitadel.New(zitadelDomain), oauth.DefaultAuthorization(zitadelKeyPath))
	} else {
		// Fallback: introspection without service account credentials (requires public introspection endpoint)
		authz, err = authorization.New(ctx, zitadel.New(zitadelDomain), oauth.DefaultAuthorization(""))
	}
	if err != nil {
		log.Fatalf("Failed to initialize Zitadel authorization: %v", err)
	}

	// Create service
	gardenSvc := service.NewGardenService(db)

	// Create mux and register service with Zitadel authentication
	mux := http.NewServeMux()

	path, handler := gardenv1connect.NewGardenServiceHandler(
		gardenSvc,
		connect.WithInterceptors(auth.ConnectInterceptor(authz)),
	)
	mux.Handle(path, handler)

	// Add CORS middleware
	corsHandler := cors(mux)

	// Start server
	addr := ":8080"
	fmt.Printf("✅ Garden API listening on %s\n", addr)
	fmt.Printf("🔐 Authentication: Zitadel OAuth2 Introspection\n")
	fmt.Printf("   - Domain: %s\n", zitadelDomain)
	if zitadelKeyPath != "" {
		fmt.Printf("   - Key: %s\n", zitadelKeyPath)
	}

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
