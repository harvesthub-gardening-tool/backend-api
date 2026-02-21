package auth

import (
	"context"
	"testing"

	authctx "harvest-hub/api/internal/auth/context"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// NewTestGORMDB creates an in-memory SQLite database with auth tables migrated.
// Used by auth package tests. The database is closed when the test completes.
func NewTestGORMDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &HubToken{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	return db
}

// CreateTestAuthContext creates a context with auth info for use in service layer tests.
func CreateTestAuthContext(userID, username string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: username,
	})
}
