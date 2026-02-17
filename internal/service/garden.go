package service

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	gardenv1 "github.com/harvesthub-gardening-tool/protos-go/garden/v1"
	"harvest-hub/api/internal/auth"
)

// DB abstracts database operations for testability.
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type GardenService struct {
	db DB
}

func NewGardenService(db DB) *GardenService {
	return &GardenService{db: db}
}

func (s *GardenService) InsertSensorData(
	ctx context.Context,
	req *connect.Request[gardenv1.InsertSensorDataRequest],
) (*connect.Response[gardenv1.InsertSensorDataResponse], error) {
	msg := req.Msg

	// Get the authenticated user/service account from context
	// This is what you wanted - user info is in the context!
	userID := auth.GetUserID(ctx)
	isServiceAccount := auth.IsServiceAccount(ctx)

	// Log who's inserting data
	fmt.Printf("Insert from: userID=%s, isServiceAccount=%v\n", userID, isServiceAccount)

	query := `
INSERT INTO sensor_data (node_id, time, temperature, humidity, soil_moisture)
VALUES ($1, $2, $3, $4, $5)
`

	timestamp := time.UnixMilli(msg.Timestamp)
	if msg.Timestamp == 0 {
		timestamp = time.Now()
	}

	_, err := s.db.Exec(ctx, query,
		msg.NodeId,
		timestamp,
		msg.Temperature,
		msg.Humidity,
		msg.SoilMoisture,
	)

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to insert data: %w", err))
	}

	return connect.NewResponse(&gardenv1.InsertSensorDataResponse{
		Success: true,
		Message: fmt.Sprintf("Data inserted by %s", userID),
	}), nil
}

func (s *GardenService) GetSummary(
	ctx context.Context,
	req *connect.Request[gardenv1.GetSummaryRequest],
) (*connect.Response[gardenv1.GetSummaryResponse], error) {
	msg := req.Msg

	// Get the authenticated user from context
	userID := auth.GetUserID(ctx)
	username := auth.GetUsername(ctx)

	// Log who's reading data
	fmt.Printf("GetSummary from: userID=%s, username=%s\n", userID, username)

	// In multi-tenant: filter by userID
	// For now: return all data

	hours := int32(24)
	if msg.Hours != nil && *msg.Hours > 0 {
		hours = *msg.Hours
	}

	// Start with the base query and arguments
	query := `
SELECT 
    node_id,
    time_bucket('15 minutes', time) AS interval,
    AVG(temperature) AS avg_temp,
    AVG(humidity) AS avg_hum,
    AVG(soil_moisture) AS avg_soil
FROM sensor_data
WHERE time > NOW() - make_interval(hours => $1)
`
	args := []any{hours}
	paramCount := 1

	// Append node_id filter if provided
	if msg.NodeId != nil && *msg.NodeId != "" {
		paramCount++
		query += fmt.Sprintf(" AND node_id = $%d", paramCount)
		args = append(args, *msg.NodeId)
	}

	query += `
GROUP BY node_id, interval
ORDER BY interval DESC
`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query failed: %w", err))
	}
	defer rows.Close()

	var summaries []*gardenv1.SensorSummary

	for rows.Next() {
		var nodeID string
		var interval time.Time
		var avgTemp, avgHum, avgSoil float64

		if err := rows.Scan(&nodeID, &interval, &avgTemp, &avgHum, &avgSoil); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan failed: %w", err))
		}

		summaries = append(summaries, &gardenv1.SensorSummary{
			NodeId:          nodeID,
			IntervalStart:   interval.UnixMilli(),
			AvgTemperature:  avgTemp,
			AvgHumidity:     avgHum,
			AvgSoilMoisture: avgSoil,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("rows error: %w", err))
	}

	return connect.NewResponse(&gardenv1.GetSummaryResponse{
		Summaries: summaries,
	}), nil
}
