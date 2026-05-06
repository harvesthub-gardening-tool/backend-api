package service

import (
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	auth "harvest-hub/api/internal/auth"
)

func TestOwnedNodeTargetQueryUsesRowLookupWhenLocked(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open dry-run postgres db: %v", err)
	}

	stmt := ownedNodeTargetQuery(db, 1, 2, "node-1", true).First(&auth.SensorNode{}).Statement
	sql := stmt.SQL.String()

	if !strings.Contains(sql, "FOR UPDATE") {
		t.Fatalf("expected locked row lookup to include FOR UPDATE, got %q", sql)
	}
	if strings.Contains(strings.ToLower(sql), "count(") {
		t.Fatalf("locked ownership verification must not use aggregate count query, got %q", sql)
	}
}
