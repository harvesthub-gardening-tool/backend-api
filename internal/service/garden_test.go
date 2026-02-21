package service_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	sqlmock "github.com/DATA-DOG/go-sqlmock"
	gardenv1 "github.com/harvesthub-gardening-tool/protos-go/garden/v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"harvest-hub/api/internal/service"
)

func ptr[T any](v T) *T { return &v }

// newTestDB creates a GORM DB backed by an in-process SQL mock.
// GORM's postgres dialector translates ? → $N, but sqlmock matches queries
// by regex so ExpectExec/ExpectQuery patterns only need to identify the statement.
//
// Note: database/sql converts int32 → int64 before the driver sees the value,
// so WithArgs must use int64 for integer parameters.
func newTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return db, mock
}

func TestInsertSensorData(t *testing.T) {
	tests := []struct {
		name    string
		req     *gardenv1.InsertSensorDataRequest
		dbErr   error
		wantErr bool
	}{
		{
			name: "successful insert with timestamp",
			req: &gardenv1.InsertSensorDataRequest{
				NodeId:       "sensor-01",
				Temperature:  22.5,
				Humidity:     65.0,
				SoilMoisture: 45.0,
				Timestamp:    1698765432000,
			},
		},
		{
			name: "successful insert without timestamp (defaults to now)",
			req: &gardenv1.InsertSensorDataRequest{
				NodeId:       "sensor-02",
				Temperature:  18.0,
				Humidity:     70.0,
				SoilMoisture: 50.0,
				Timestamp:    0,
			},
		},
		{
			name: "db error propagates",
			req: &gardenv1.InsertSensorDataRequest{
				NodeId:       "sensor-01",
				Temperature:  22.5,
				Humidity:     65.0,
				SoilMoisture: 45.0,
				Timestamp:    1698765432000,
			},
			dbErr:   sqlmock.ErrCancelled,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newTestDB(t)

			exp := mock.ExpectExec("INSERT INTO sensor_data").
				WithArgs(tt.req.NodeId, sqlmock.AnyArg(), tt.req.Temperature, tt.req.Humidity, tt.req.SoilMoisture)

			if tt.dbErr != nil {
				exp.WillReturnError(tt.dbErr)
			} else {
				exp.WillReturnResult(sqlmock.NewResult(1, 1))
			}

			svc := service.NewGardenService(db)
			resp, err := svc.InsertSensorData(context.Background(), connect.NewRequest(tt.req))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if connect.CodeOf(err) != connect.CodeInternal {
					t.Errorf("expected CodeInternal, got %v", connect.CodeOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !resp.Msg.Success {
				t.Error("expected Success=true")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet expectations: %v", err)
			}
		})
	}
}

func TestInsertSensorData_TimestampDefault(t *testing.T) {
	db, mock := newTestDB(t)

	mock.ExpectExec("INSERT INTO sensor_data").
		WithArgs("node-1", sqlmock.AnyArg(), float64(20), float64(60), float64(40)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	svc := service.NewGardenService(db)
	_, err := svc.InsertSensorData(context.Background(), connect.NewRequest(&gardenv1.InsertSensorDataRequest{
		NodeId:       "node-1",
		Temperature:  20,
		Humidity:     60,
		SoilMoisture: 40,
		Timestamp:    0,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSummary(t *testing.T) {
	now := time.Now().Truncate(15 * time.Minute)
	cols := []string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}

	tests := []struct {
		name      string
		req       *gardenv1.GetSummaryRequest
		mockSetup func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name: "default hours, no node filter",
			req:  &gardenv1.GetSummaryRequest{},
			mockSetup: func(mock sqlmock.Sqlmock) {
				// database/sql converts int32 → int64 before the driver sees it
				mock.ExpectQuery("SELECT").WithArgs(int64(24)).
					WillReturnRows(sqlmock.NewRows(cols).
						AddRow("sensor-01", now, 22.5, 65.0, 45.0).
						AddRow("sensor-01", now.Add(-15*time.Minute), 21.0, 63.0, 44.0))
			},
			wantCount: 2,
		},
		{
			name: "custom hours with node filter",
			req:  &gardenv1.GetSummaryRequest{NodeId: ptr("sensor-01"), Hours: ptr(int32(6))},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT").WithArgs(int64(6), "sensor-01").
					WillReturnRows(sqlmock.NewRows(cols).AddRow("sensor-01", now, 22.5, 65.0, 45.0))
			},
			wantCount: 1,
		},
		{
			name: "empty result",
			req:  &gardenv1.GetSummaryRequest{NodeId: ptr("nonexistent")},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT").WithArgs(int64(24), "nonexistent").
					WillReturnRows(sqlmock.NewRows(cols))
			},
			wantCount: 0,
		},
		{
			name: "db query error",
			req:  &gardenv1.GetSummaryRequest{},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT").WithArgs(int64(24)).
					WillReturnError(sqlmock.ErrCancelled)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newTestDB(t)
			tt.mockSetup(mock)

			svc := service.NewGardenService(db)
			resp, err := svc.GetSummary(context.Background(), connect.NewRequest(tt.req))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if connect.CodeOf(err) != connect.CodeInternal {
					t.Errorf("expected CodeInternal, got %v", connect.CodeOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := len(resp.Msg.Summaries); got != tt.wantCount {
				t.Errorf("got %d summaries, want %d", got, tt.wantCount)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet expectations: %v", err)
			}
		})
	}
}

func TestGetSummary_HoursDefault(t *testing.T) {
	db, mock := newTestDB(t)

	mock.ExpectQuery("SELECT").WithArgs(int64(24)).
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}))

	svc := service.NewGardenService(db)
	_, err := svc.GetSummary(context.Background(), connect.NewRequest(&gardenv1.GetSummaryRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSummary_ZeroHoursDefaultsTo24(t *testing.T) {
	db, mock := newTestDB(t)

	mock.ExpectQuery("SELECT").WithArgs(int64(24)).
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}))

	svc := service.NewGardenService(db)
	_, err := svc.GetSummary(context.Background(), connect.NewRequest(&gardenv1.GetSummaryRequest{
		Hours: ptr(int32(0)),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
