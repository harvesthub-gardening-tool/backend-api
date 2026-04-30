package service_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	sqlmock "github.com/DATA-DOG/go-sqlmock"
	gardenv2 "github.com/harvesthub-gardening-tool/protos-go/garden/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	authctx "harvest-hub/api/internal/auth/context"
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

// hubCtx builds a context as the JWT middleware would for a v2 hub token:
// service account (empty Username) with a HubID claim. UserID is also set
// because the issuer of the hub token is recorded as the owning user.
func hubCtx(userID, hubID string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: "",
		HubID:    hubID,
	})
}

// userCtx builds a context as the JWT middleware would for a logged-in user.
func userCtx(userID string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: "alice@example.com",
	})
}

// sensorNodeCols mirrors the columns scanned by GORM's First() into auth.SensorNode.
var sensorNodeCols = []string{"id", "node_id", "hub_id", "name", "location", "created_at", "updated_at"}

func TestInsertSensorData(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		req       *gardenv2.InsertSensorDataRequest
		mockSetup func(sqlmock.Sqlmock)
		wantErr   bool
		wantCode  connect.Code
	}{
		{
			name: "successful insert: new sensor node binds to caller hub",
			ctx:  hubCtx("1", "42"),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "sensor-01", AirTemperature: 22.5, AirPressure: 101325.0,
				AirHumidity: 65.0, SoilTemperature: 19.5, SoilHumidity: 45.0,
				Timestamp: 1698765432000,
			},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(`SELECT .* FROM "sensor_nodes"`).
					WithArgs("sensor-01", 1).
					WillReturnRows(sqlmock.NewRows(sensorNodeCols))
				mock.ExpectBegin()
				mock.ExpectQuery(`INSERT INTO "sensor_nodes"`).
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
				mock.ExpectCommit()
				mock.ExpectExec("INSERT INTO sensor_data").
					WithArgs("sensor-01", sqlmock.AnyArg(), 22.5, 101325.0, 65.0, 19.5, 45.0).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
		},
		{
			name: "successful insert: existing node already bound to caller hub",
			ctx:  hubCtx("1", "42"),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "sensor-02", AirTemperature: 18.0, AirPressure: 100900.0,
				AirHumidity: 70.0, SoilTemperature: 17.5, SoilHumidity: 50.0,
				Timestamp: 0,
			},
			mockSetup: func(mock sqlmock.Sqlmock) {
				hubID := int64(42)
				mock.ExpectQuery(`SELECT .* FROM "sensor_nodes"`).
					WithArgs("sensor-02", 1).
					WillReturnRows(sqlmock.NewRows(sensorNodeCols).
						AddRow(1, "sensor-02", hubID, "", "", time.Now(), time.Now()))
				mock.ExpectExec("INSERT INTO sensor_data").
					WithArgs("sensor-02", sqlmock.AnyArg(), 18.0, 100900.0, 70.0, 17.5, 50.0).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
		},
		{
			name: "permission denied: caller is a user, not a hub",
			ctx:  userCtx("1"),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "sensor-01", AirTemperature: 22.5, AirHumidity: 65.0, SoilHumidity: 45.0,
			},
			mockSetup: func(_ sqlmock.Sqlmock) {},
			wantErr:   true,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name: "permission denied: hub token without HubID claim",
			ctx:  hubCtx("1", ""),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "sensor-01", AirTemperature: 22.5, AirHumidity: 65.0, SoilHumidity: 45.0,
			},
			mockSetup: func(_ sqlmock.Sqlmock) {},
			wantErr:   true,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name: "permission denied: node already bound to a different hub (cross-hub spoof)",
			ctx:  hubCtx("1", "42"),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "sensor-99", AirTemperature: 22.5, AirHumidity: 65.0, SoilHumidity: 45.0,
			},
			mockSetup: func(mock sqlmock.Sqlmock) {
				otherHub := int64(99)
				mock.ExpectQuery(`SELECT .* FROM "sensor_nodes"`).
					WithArgs("sensor-99", 1).
					WillReturnRows(sqlmock.NewRows(sensorNodeCols).
						AddRow(1, "sensor-99", otherHub, "", "", time.Now(), time.Now()))
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "invalid argument: empty node_id",
			ctx:  hubCtx("1", "42"),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "", AirTemperature: 1, AirHumidity: 2, SoilHumidity: 3,
			},
			mockSetup: func(_ sqlmock.Sqlmock) {},
			wantErr:   true,
			wantCode:  connect.CodeInvalidArgument,
		},
		{
			name: "internal error: db error on sensor_data insert propagates",
			ctx:  hubCtx("1", "42"),
			req: &gardenv2.InsertSensorDataRequest{
				NodeId: "sensor-01", AirTemperature: 22.5, AirPressure: 101325.0,
				AirHumidity: 65.0, SoilTemperature: 19.5, SoilHumidity: 45.0,
				Timestamp: 1698765432000,
			},
			mockSetup: func(mock sqlmock.Sqlmock) {
				hubID := int64(42)
				mock.ExpectQuery(`SELECT .* FROM "sensor_nodes"`).
					WithArgs("sensor-01", 1).
					WillReturnRows(sqlmock.NewRows(sensorNodeCols).
						AddRow(1, "sensor-01", hubID, "", "", time.Now(), time.Now()))
				mock.ExpectExec("INSERT INTO sensor_data").
					WithArgs("sensor-01", sqlmock.AnyArg(), 22.5, 101325.0, 65.0, 19.5, 45.0).
					WillReturnError(sqlmock.ErrCancelled)
			},
			wantErr:  true,
			wantCode: connect.CodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newTestDB(t)
			tt.mockSetup(mock)

			svc := service.NewGardenService(db)
			resp, err := svc.InsertSensorData(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if connect.CodeOf(err) != tt.wantCode {
					t.Errorf("expected %v, got %v: %v", tt.wantCode, connect.CodeOf(err), err)
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

func TestGetSummary(t *testing.T) {
	now := time.Now().Truncate(15 * time.Minute)
	cols := []string{
		"node_id",
		"hub_id",
		"interval",
		"avg_air_temperature",
		"avg_air_pressure",
		"avg_air_humidity",
		"avg_soil_temperature",
		"avg_soil_humidity",
		"max_air_temperature",
	}

	tests := []struct {
		name      string
		ctx       context.Context
		req       *gardenv2.GetSummaryRequest
		mockSetup func(sqlmock.Sqlmock)
		wantCount int
		wantHubID string
		wantErr   bool
		wantCode  connect.Code
	}{
		{
			name: "user: default hours, no node filter, scoped by user_id",
			ctx:  userCtx("7"),
			req:  &gardenv2.GetSummaryRequest{},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM sensor_data sd").
					WithArgs(int64(24), int64(7)).
					WillReturnRows(sqlmock.NewRows(cols).
						AddRow("sensor-01", int64(42), now, 22.5, 101325.0, 65.0, 19.5, 45.0, 23.0).
						AddRow("sensor-01", int64(42), now.Add(-15*time.Minute), 21.0, 101100.0, 63.0, 18.5, 44.0, 21.5))
			},
			wantCount: 2,
			wantHubID: "42",
		},
		{
			name: "user: custom hours with node filter",
			ctx:  userCtx("7"),
			req:  &gardenv2.GetSummaryRequest{NodeId: ptr("sensor-01"), Hours: ptr(int32(6))},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM sensor_data sd").
					WithArgs(int64(6), int64(7), "sensor-01").
					WillReturnRows(sqlmock.NewRows(cols).AddRow("sensor-01", int64(42), now, 22.5, 101325.0, 65.0, 19.5, 45.0, 23.0))
			},
			wantCount: 1,
		},
		{
			name: "user: hub_id filter narrows to a single hub",
			ctx:  userCtx("7"),
			req:  &gardenv2.GetSummaryRequest{HubId: ptr("42")},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM sensor_data sd").
					WithArgs(int64(24), int64(7), int64(42)).
					WillReturnRows(sqlmock.NewRows(cols).AddRow("sensor-01", int64(42), now, 22.5, 101325.0, 65.0, 19.5, 45.0, 23.0))
			},
			wantCount: 1,
		},
		{
			name:      "user: invalid hub_id rejected",
			ctx:       userCtx("7"),
			req:       &gardenv2.GetSummaryRequest{HubId: ptr("not-a-number")},
			mockSetup: func(_ sqlmock.Sqlmock) {},
			wantErr:   true,
			wantCode:  connect.CodeInvalidArgument,
		},
		{
			name: "user: empty result still scoped by user_id",
			ctx:  userCtx("7"),
			req:  &gardenv2.GetSummaryRequest{NodeId: ptr("nonexistent")},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM sensor_data sd").
					WithArgs(int64(24), int64(7), "nonexistent").
					WillReturnRows(sqlmock.NewRows(cols))
			},
			wantCount: 0,
		},
		{
			name: "hub: scoped to caller's hub_id in addition to user_id",
			ctx:  hubCtx("7", "42"),
			req:  &gardenv2.GetSummaryRequest{},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM sensor_data sd").
					WithArgs(int64(24), int64(7), int64(42)).
					WillReturnRows(sqlmock.NewRows(cols).
						AddRow("sensor-01", int64(42), now, 22.5, 101325.0, 65.0, 19.5, 45.0, 23.0))
			},
			wantCount: 1,
		},
		{
			name:      "unauthenticated: no auth info in context",
			ctx:       context.Background(),
			req:       &gardenv2.GetSummaryRequest{},
			mockSetup: func(_ sqlmock.Sqlmock) {},
			wantErr:   true,
			wantCode:  connect.CodeUnauthenticated,
		},
		{
			name:      "permission denied: hub token without HubID claim",
			ctx:       hubCtx("7", ""),
			req:       &gardenv2.GetSummaryRequest{},
			mockSetup: func(_ sqlmock.Sqlmock) {},
			wantErr:   true,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name: "internal: db query error propagates",
			ctx:  userCtx("7"),
			req:  &gardenv2.GetSummaryRequest{},
			mockSetup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("FROM sensor_data sd").
					WithArgs(int64(24), int64(7)).
					WillReturnError(sqlmock.ErrCancelled)
			},
			wantErr:  true,
			wantCode: connect.CodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newTestDB(t)
			tt.mockSetup(mock)

			svc := service.NewGardenService(db)
			resp, err := svc.GetSummary(tt.ctx, connect.NewRequest(tt.req))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if connect.CodeOf(err) != tt.wantCode {
					t.Errorf("expected %v, got %v: %v", tt.wantCode, connect.CodeOf(err), err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := len(resp.Msg.Summaries); got != tt.wantCount {
				t.Errorf("got %d summaries, want %d", got, tt.wantCount)
			}
			if tt.wantHubID != "" && len(resp.Msg.Summaries) > 0 {
				if got := resp.Msg.Summaries[0].HubId; got != tt.wantHubID {
					t.Errorf("HubId: got %q, want %q", got, tt.wantHubID)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet expectations: %v", err)
			}
		})
	}
}

func TestGetSummary_HoursDefault(t *testing.T) {
	db, mock := newTestDB(t)

	mock.ExpectQuery("FROM sensor_data sd").WithArgs(int64(24), int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"node_id",
			"hub_id",
			"interval",
			"avg_air_temperature",
			"avg_air_pressure",
			"avg_air_humidity",
			"avg_soil_temperature",
			"avg_soil_humidity",
			"max_air_temperature",
		}))

	svc := service.NewGardenService(db)
	_, err := svc.GetSummary(userCtx("1"), connect.NewRequest(&gardenv2.GetSummaryRequest{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSummary_ZeroHoursDefaultsTo24(t *testing.T) {
	db, mock := newTestDB(t)

	mock.ExpectQuery("FROM sensor_data sd").WithArgs(int64(24), int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"node_id",
			"hub_id",
			"interval",
			"avg_air_temperature",
			"avg_air_pressure",
			"avg_air_humidity",
			"avg_soil_temperature",
			"avg_soil_humidity",
			"max_air_temperature",
		}))

	svc := service.NewGardenService(db)
	_, err := svc.GetSummary(userCtx("1"), connect.NewRequest(&gardenv2.GetSummaryRequest{
		Hours: ptr(int32(0)),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
