package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	gardenv2 "github.com/harvesthub-gardening-tool/protos-go/garden/v2"
	"gorm.io/gorm"

	auth "harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
)

type GardenService struct {
	db *gorm.DB
}

func NewGardenService(db *gorm.DB) *GardenService {
	return &GardenService{db: db}
}

// InsertSensorData stores a sensor reading attributed to the caller's hub.
// Authorization (enforced by middleware + here):
//   - Caller must be a service account (hub token).
//   - Caller's token must carry a non-empty HubID claim (issued by
//     auth.v2/ClaimHubToken). Tokens without HubID are rejected.
//   - The sensor_node identified by NodeId is bound to the caller's hub on first
//     sight. Subsequent inserts for the same NodeId from a different hub are
//     rejected with PermissionDenied (anti-spoofing across hubs).
func (s *GardenService) InsertSensorData(
	ctx context.Context,
	req *connect.Request[gardenv2.InsertSensorDataRequest],
) (*connect.Response[gardenv2.InsertSensorDataResponse], error) {
	msg := req.Msg

	if !authctx.IsServiceAccount(ctx) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only hub tokens may insert sensor data"))
	}
	hubIDStr := authctx.GetHubID(ctx)
	if hubIDStr == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("hub token is missing hub binding (re-claim via v2)"))
	}
	var hubID uint
	if _, err := fmt.Sscan(hubIDStr, &hubID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("invalid hub id in token: %w", err))
	}

	if msg.NodeId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("node_id is required"))
	}

	if err := s.bindNodeToHub(ctx, msg.NodeId, hubID); err != nil {
		return nil, err
	}

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

	return connect.NewResponse(&gardenv2.InsertSensorDataResponse{
		Success: true,
		Message: fmt.Sprintf("Data inserted for node %s", msg.NodeId),
	}), nil
}

// bindNodeToHub claims an unowned sensor_node for the caller's hub, or verifies
// an existing binding matches. Returns PermissionDenied if the node is already
// bound to a different hub (cross-hub spoofing attempt).
func (s *GardenService) bindNodeToHub(ctx context.Context, nodeID string, hubID uint) error {
	var node auth.SensorNode
	err := s.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&node).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		newNode := auth.SensorNode{NodeID: nodeID, HubID: &hubID}
		if err := s.db.WithContext(ctx).Create(&newNode).Error; err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to register sensor node: %w", err))
		}
		return nil
	case err != nil:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load sensor node: %w", err))
	}

	if node.HubID != nil && *node.HubID != hubID {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("node %s is bound to a different hub", nodeID))
	}
	if node.HubID == nil {
		if err := s.db.WithContext(ctx).Model(&node).Update("hub_id", hubID).Error; err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to bind sensor node: %w", err))
		}
	}
	return nil
}

// sensorSummaryRow is the scan target for GetSummary's aggregation query.
type sensorSummaryRow struct {
	NodeID   string    `gorm:"column:node_id"`
	HubID    uint      `gorm:"column:hub_id"`
	Interval time.Time `gorm:"column:interval"`
	AvgTemp  float64   `gorm:"column:avg_temp"`
	AvgHum   float64   `gorm:"column:avg_hum"`
	AvgSoil  float64   `gorm:"column:avg_soil"`
}

// GetSummary returns time-bucketed averages restricted to data the caller owns.
// Ownership rules:
//   - User tokens (non-empty username): see all hubs owned by the user, with an
//     optional hub_id filter to narrow to a single hub.
//   - Hub tokens (with HubID claim): see only their own hub; user-supplied
//     hub_id is ignored.
//   - Hub tokens without a HubID claim are rejected.
//
// The JOIN through sensor_nodes → hubs is the sole authorization boundary; rows
// in sensor_data without a corresponding sensor_nodes binding are invisible.
func (s *GardenService) GetSummary(
	ctx context.Context,
	req *connect.Request[gardenv2.GetSummaryRequest],
) (*connect.Response[gardenv2.GetSummaryResponse], error) {
	msg := req.Msg

	userIDStr := authctx.GetUserID(ctx)
	if userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing user id in token"))
	}
	var userID uint
	if _, err := fmt.Sscan(userIDStr, &userID); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid user id in token: %w", err))
	}

	isService := authctx.IsServiceAccount(ctx)
	hubIDStr := authctx.GetHubID(ctx)
	if isService && hubIDStr == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("hub token is missing hub binding (re-claim via v2)"))
	}

	hours := int32(24)
	if msg.Hours != nil && *msg.Hours > 0 {
		hours = *msg.Hours
	}

	query := `
SELECT
    sd.node_id,
    h.id                  AS hub_id,
    time_bucket('15 minutes', sd.time) AS interval,
    AVG(sd.temperature)   AS avg_temp,
    AVG(sd.humidity)      AS avg_hum,
    AVG(sd.soil_moisture) AS avg_soil
FROM sensor_data sd
JOIN sensor_nodes sn ON sn.node_id = sd.node_id
JOIN hubs h          ON h.id = sn.hub_id
WHERE sd.time > NOW() - make_interval(hours => ?)
  AND h.user_id = ?`

	args := []any{hours, userID}

	if isService {
		var hubID uint
		if _, err := fmt.Sscan(hubIDStr, &hubID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("invalid hub id in token: %w", err))
		}
		query += " AND h.id = ?"
		args = append(args, hubID)
	} else if msg.HubId != nil && *msg.HubId != "" {
		var hubID uint
		if _, err := fmt.Sscan(*msg.HubId, &hubID); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid hub_id: %w", err))
		}
		query += " AND h.id = ?"
		args = append(args, hubID)
	}

	if msg.NodeId != nil && *msg.NodeId != "" {
		query += " AND sd.node_id = ?"
		args = append(args, *msg.NodeId)
	}

	query += `
GROUP BY sd.node_id, h.id, interval
ORDER BY interval DESC`

	var rows []sensorSummaryRow
	if result := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows); result.Error != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query failed: %w", result.Error))
	}

	summaries := make([]*gardenv2.SensorSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, &gardenv2.SensorSummary{
			NodeId:          row.NodeID,
			HubId:           fmt.Sprint(row.HubID),
			IntervalStart:   row.Interval.UnixMilli(),
			AvgTemperature:  row.AvgTemp,
			AvgHumidity:     row.AvgHum,
			AvgSoilMoisture: row.AvgSoil,
		})
	}

	return connect.NewResponse(&gardenv2.GetSummaryResponse{
		Summaries: summaries,
	}), nil
}
