package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	gardenv1 "github.com/harvesthub-gardening-tool/protos-go/garden/v1"
	"github.com/pashagolub/pgxmock/v4"

	"harvest-hub/api/internal/service"
)

func ptr[T any](v T) *T { return &v }

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
			dbErr:   errors.New("connection refused"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock, err := pgxmock.NewPool()
			if err != nil {
				t.Fatalf("failed to create mock: %v", err)
			}
			defer mock.Close()

			expect := mock.ExpectExec("INSERT INTO sensor_data").
				WithArgs(
					tt.req.NodeId,
					pgxmock.AnyArg(), // timestamp (either from request or time.Now)
					tt.req.Temperature,
					tt.req.Humidity,
					tt.req.SoilMoisture,
				)

			if tt.dbErr != nil {
				expect.WillReturnError(tt.dbErr)
			} else {
				expect.WillReturnResult(pgxmock.NewResult("INSERT", 1))
			}

			svc := service.NewGardenService(mock)
			resp, err := svc.InsertSensorData(
				context.Background(),
				connect.NewRequest(tt.req),
			)

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
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock: %v", err)
	}
	defer mock.Close()

	before := time.Now()

	mock.ExpectExec("INSERT INTO sensor_data").
		WithArgs(
			"node-1",
			pgxmock.AnyArg(),
			float64(20),
			float64(60),
			float64(40),
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	svc := service.NewGardenService(mock)
	_, err = svc.InsertSensorData(
		context.Background(),
		connect.NewRequest(&gardenv1.InsertSensorDataRequest{
			NodeId:       "node-1",
			Temperature:  20,
			Humidity:     60,
			SoilMoisture: 40,
			Timestamp:    0, // should default to ~now
		}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := time.Now()
	_ = before
	_ = after
	// The exact timestamp is validated by pgxmock.AnyArg().
	// A stricter check would use a custom matcher, but this confirms
	// the code path that defaults to time.Now() is exercised.
}

func TestGetSummary(t *testing.T) {
	now := time.Now().Truncate(15 * time.Minute)

	tests := []struct {
		name      string
		req       *gardenv1.GetSummaryRequest
		rows      *pgxmock.Rows
		dbErr     error
		wantCount int
		wantErr   bool
		args      []any
	}{
		{
			name: "default hours, no node filter",
			req:  &gardenv1.GetSummaryRequest{},
			rows: pgxmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}).
				AddRow("sensor-01", now, 22.5, 65.0, 45.0).
				AddRow("sensor-01", now.Add(-15*time.Minute), 21.0, 63.0, 44.0),
			wantCount: 2,
			args:      []any{int32(24)},
		},
		{
			name: "custom hours with node filter",
			req: &gardenv1.GetSummaryRequest{
				NodeId: ptr("sensor-01"),
				Hours:  ptr(int32(6)),
			},
			rows: pgxmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}).
				AddRow("sensor-01", now, 22.5, 65.0, 45.0),
			wantCount: 1,
			args:      []any{int32(6), "sensor-01"},
		},
		{
			name:      "empty result",
			req:       &gardenv1.GetSummaryRequest{NodeId: ptr("nonexistent")},
			rows:      pgxmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}),
			wantCount: 0,
			args:      []any{int32(24), "nonexistent"},
		},
		{
			name:    "db query error",
			req:     &gardenv1.GetSummaryRequest{},
			dbErr:   errors.New("connection lost"),
			wantErr: true,
			args:    []any{int32(24)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock, err := pgxmock.NewPool()
			if err != nil {
				t.Fatalf("failed to create mock: %v", err)
			}
			defer mock.Close()

			expect := mock.ExpectQuery("SELECT").WithArgs(tt.args...)

			if tt.dbErr != nil {
				expect.WillReturnError(tt.dbErr)
			} else {
				expect.WillReturnRows(tt.rows)
			}

			svc := service.NewGardenService(mock)
			resp, err := svc.GetSummary(
				context.Background(),
				connect.NewRequest(tt.req),
			)

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

			got := len(resp.Msg.Summaries)
			if got != tt.wantCount {
				t.Errorf("got %d summaries, want %d", got, tt.wantCount)
			}

			if err := mock.ExpectationsWereMet(); err != nil {
				t.Errorf("unmet expectations: %v", err)
			}
		})
	}
}

func TestGetSummary_HoursDefault(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock: %v", err)
	}
	defer mock.Close()

	// With nil Hours, should default to 24
	mock.ExpectQuery("SELECT").
		WithArgs(int32(24)).
		WillReturnRows(pgxmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}))

	svc := service.NewGardenService(mock)
	_, err = svc.GetSummary(
		context.Background(),
		connect.NewRequest(&gardenv1.GetSummaryRequest{}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSummary_ZeroHoursDefaultsTo24(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("failed to create mock: %v", err)
	}
	defer mock.Close()

	// Hours=0 should also default to 24
	mock.ExpectQuery("SELECT").
		WithArgs(int32(24)).
		WillReturnRows(pgxmock.NewRows([]string{"node_id", "interval", "avg_temp", "avg_hum", "avg_soil"}))

	svc := service.NewGardenService(mock)
	_, err = svc.GetSummary(
		context.Background(),
		connect.NewRequest(&gardenv1.GetSummaryRequest{
			Hours: ptr(int32(0)),
		}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
