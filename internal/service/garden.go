package service

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	gardenv1 "github.com/harvesthub-gardening-tool/protos-go/garden/v1"
	"gorm.io/gorm"

	authctx "harvest-hub/api/internal/auth/context"
)

type GardenService struct {
	db *gorm.DB
}

func NewGardenService(db *gorm.DB) *GardenService {
	return &GardenService{db: db}
}

func (s *GardenService) InsertSensorData(
	ctx context.Context,
	req *connect.Request[gardenv1.InsertSensorDataRequest],
) (*connect.Response[gardenv1.InsertSensorDataResponse], error) {
	msg := req.Msg

	userID := authctx.GetUserID(ctx)
	isServiceAccount := authctx.IsServiceAccount(ctx)
	fmt.Printf("Insert from: userID=%s, isServiceAccount=%v\n", userID, isServiceAccount)

	timestamp := time.UnixMilli(msg.Timestamp)
	if msg.Timestamp == 0 {
		timestamp = time.Now()
	}

	result := s.db.WithContext(ctx).Exec(
		`INSERT INTO sensor_data (node_id, time, temperature, humidity, soil_moisture) VALUES (?, ?, ?, ?, ?)`,
		msg.NodeId, timestamp, msg.Temperature, msg.Humidity, msg.SoilMoisture,
	)
	if result.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to insert data: %w", result.Error))
	}

	return connect.NewResponse(&gardenv1.InsertSensorDataResponse{
		Success: true,
		Message: fmt.Sprintf("Data inserted by %s", userID),
	}), nil
}

// sensorSummaryRow is the scan target for GetSummary's aggregation query.
type sensorSummaryRow struct {
	NodeID   string    `gorm:"column:node_id"`
	Interval time.Time `gorm:"column:interval"`
	AvgTemp  float64   `gorm:"column:avg_temp"`
	AvgHum   float64   `gorm:"column:avg_hum"`
	AvgSoil  float64   `gorm:"column:avg_soil"`
}

func (s *GardenService) GetSummary(
	ctx context.Context,
	req *connect.Request[gardenv1.GetSummaryRequest],
) (*connect.Response[gardenv1.GetSummaryResponse], error) {
	msg := req.Msg

	userID := authctx.GetUserID(ctx)
	username := authctx.GetUsername(ctx)
	fmt.Printf("GetSummary from: userID=%s, username=%s\n", userID, username)

	hours := int32(24)
	if msg.Hours != nil && *msg.Hours > 0 {
		hours = *msg.Hours
	}

	query := `
SELECT
    node_id,
    time_bucket('15 minutes', time) AS interval,
    AVG(temperature) AS avg_temp,
    AVG(humidity)    AS avg_hum,
    AVG(soil_moisture) AS avg_soil
FROM sensor_data
WHERE time > NOW() - make_interval(hours => ?)`

	args := []any{hours}

	if msg.NodeId != nil && *msg.NodeId != "" {
		query += " AND node_id = ?"
		args = append(args, *msg.NodeId)
	}

	query += `
GROUP BY node_id, interval
ORDER BY interval DESC`

	var rows []sensorSummaryRow
	if result := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows); result.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query failed: %w", result.Error))
	}

	summaries := make([]*gardenv1.SensorSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, &gardenv1.SensorSummary{
			NodeId:          row.NodeID,
			IntervalStart:   row.Interval.UnixMilli(),
			AvgTemperature:  row.AvgTemp,
			AvgHumidity:     row.AvgHum,
			AvgSoilMoisture: row.AvgSoil,
		})
	}

	return connect.NewResponse(&gardenv1.GetSummaryResponse{
		Summaries: summaries,
	}), nil
}
