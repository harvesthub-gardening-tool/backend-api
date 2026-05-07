package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	chatv1 "github.com/harvesthub-gardening-tool/protos-go/chat/v1"
	"gorm.io/gorm"

	authctx "harvest-hub/api/internal/auth/context"
)

const (
	defaultMistralBaseURL      = "https://api.mistral.ai"
	defaultMistralAgentVersion = 1
	maxChatMessageLength       = 2000
	maxPlantContextItems       = 40
)

type ChatService struct {
	db       *gorm.DB
	provider chatProvider
}

type chatProvider interface {
	Send(ctx context.Context, userMessage string, contextBlock string) (string, error)
}

type ChatServiceConfig struct {
	APIKey       string
	AgentID      string
	AgentVersion int
	BaseURL      string
	HTTPClient   *http.Client
}

type MistralProvider struct {
	apiKey       string
	agentID      string
	agentVersion int
	baseURL      string
	httpClient   *http.Client
}

func NewChatService(db *gorm.DB, cfg ChatServiceConfig) *ChatService {
	return &ChatService{
		db:       db,
		provider: NewMistralProvider(cfg),
	}
}

func newChatServiceWithProvider(db *gorm.DB, provider chatProvider) *ChatService {
	return &ChatService{db: db, provider: provider}
}

func NewMistralProvider(cfg ChatServiceConfig) *MistralProvider {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultMistralBaseURL
	}
	agentVersion := cfg.AgentVersion
	if agentVersion <= 0 {
		agentVersion = defaultMistralAgentVersion
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &MistralProvider{
		apiKey:       strings.TrimSpace(cfg.APIKey),
		agentID:      strings.TrimSpace(cfg.AgentID),
		agentVersion: agentVersion,
		baseURL:      baseURL,
		httpClient:   client,
	}
}

func (s *ChatService) SendMessage(
	ctx context.Context,
	req *connect.Request[chatv1.SendMessageRequest],
) (*connect.Response[chatv1.SendMessageResponse], error) {
	if authctx.IsServiceAccount(ctx) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only user tokens may use chat"))
	}

	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.Msg.GetMessage())
	if message == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("message is required"))
	}
	if len([]rune(message)) > maxChatMessageLength {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("message must be at most %d characters", maxChatMessageLength))
	}

	contextBlock, err := s.buildGardenContext(ctx, userID, req.Msg.GetPlants())
	if err != nil {
		return nil, err
	}

	reply, err := s.provider.Send(ctx, message, contextBlock)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("assistant unavailable: %w", err))
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("assistant returned an empty response"))
	}

	return connect.NewResponse(&chatv1.SendMessageResponse{Reply: reply}), nil
}

func authenticatedUserID(ctx context.Context) (uint, error) {
	userIDStr := authctx.GetUserID(ctx)
	if userIDStr == "" {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing user id in token"))
	}
	var userID uint
	if _, err := fmt.Sscan(userIDStr, &userID); err != nil {
		return 0, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid user id in token: %w", err))
	}
	return userID, nil
}

type chatProbeContextRow struct {
	NodeID          string     `gorm:"column:node_id"`
	ProbeName       string     `gorm:"column:probe_name"`
	ProbeLocation   string     `gorm:"column:probe_location"`
	HubName         string     `gorm:"column:hub_name"`
	ReadingTime     *time.Time `gorm:"column:reading_time"`
	AirTemperature  *float64   `gorm:"column:air_temperature"`
	AirPressure     *float64   `gorm:"column:air_pressure"`
	AirHumidity     *float64   `gorm:"column:air_humidity"`
	SoilTemperature *float64   `gorm:"column:soil_temperature"`
	SoilHumidity    *float64   `gorm:"column:soil_humidity"`
}

func (s *ChatService) buildGardenContext(ctx context.Context, userID uint, plants []*chatv1.ChatPlantContext) (string, error) {
	var probes []chatProbeContextRow
	if err := s.db.WithContext(ctx).Raw(`
SELECT
    sn.node_id,
    sn.name AS probe_name,
    sn.location AS probe_location,
    h.name AS hub_name,
    latest.time AS reading_time,
    latest.air_temperature,
    latest.air_pressure,
    latest.air_humidity,
    latest.soil_temperature,
    latest.soil_humidity
FROM sensor_nodes sn
JOIN hubs h ON h.id = sn.hub_id
LEFT JOIN LATERAL (
    SELECT sd.time, sd.air_temperature, sd.air_pressure, sd.air_humidity, sd.soil_temperature, sd.soil_humidity
    FROM sensor_data sd
    WHERE sd.node_id = sn.node_id
    ORDER BY sd.time DESC
    LIMIT 1
) latest ON TRUE
WHERE h.user_id = ?
ORDER BY h.name, sn.node_id
LIMIT 50`, userID).Scan(&probes).Error; err != nil {
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load garden context: %w", err))
	}

	var builder strings.Builder
	builder.WriteString("Harvest Hub garden context for this authenticated user. Use it only as supporting context; if data is missing, say so.\n")
	writePlantContext(&builder, plants)
	writeProbeContext(&builder, probes)
	return builder.String(), nil
}

func writePlantContext(builder *strings.Builder, plants []*chatv1.ChatPlantContext) {
	builder.WriteString("Plants from the mobile garden map:\n")
	if len(plants) == 0 {
		builder.WriteString("- No plant map context was provided by the mobile app.\n")
		return
	}
	limit := len(plants)
	if limit > maxPlantContextItems {
		limit = maxPlantContextItems
	}
	for _, plant := range plants[:limit] {
		if plant == nil {
			continue
		}
		name := strings.TrimSpace(plant.GetName())
		if name == "" {
			name = "Unnamed plant"
		}
		quantity := plant.GetQuantity()
		if quantity <= 0 {
			quantity = 1
		}
		probe := strings.TrimSpace(plant.GetProbeNodeId())
		if probe == "" {
			probe = "no linked probe"
		}
		builder.WriteString(fmt.Sprintf("- %s, quantity %d, linked probe: %s.\n", name, quantity, probe))
	}
	if len(plants) > limit {
		builder.WriteString(fmt.Sprintf("- %d additional plants omitted.\n", len(plants)-limit))
	}
}

func writeProbeContext(builder *strings.Builder, probes []chatProbeContextRow) {
	builder.WriteString("Latest authorized probe readings from backend:\n")
	if len(probes) == 0 {
		builder.WriteString("- No probes are associated with this user yet.\n")
		return
	}
	for _, probe := range probes {
		label := probe.NodeID
		if strings.TrimSpace(probe.ProbeName) != "" {
			label = fmt.Sprintf("%s (%s)", probe.ProbeName, probe.NodeID)
		}
		builder.WriteString(fmt.Sprintf("- Probe %s on hub %s", label, probe.HubName))
		if strings.TrimSpace(probe.ProbeLocation) != "" {
			builder.WriteString(fmt.Sprintf(", location %s", probe.ProbeLocation))
		}
		if probe.ReadingTime == nil {
			builder.WriteString(": no sensor reading yet.\n")
			continue
		}
		builder.WriteString(fmt.Sprintf(": last reading %s", probe.ReadingTime.UTC().Format(time.RFC3339)))
		writeMetric(builder, "air temp", probe.AirTemperature, "°C")
		writeMetric(builder, "air humidity", probe.AirHumidity, "%")
		writeMetric(builder, "soil temp", probe.SoilTemperature, "°C")
		writeMetric(builder, "soil humidity", probe.SoilHumidity, "%")
		writeMetric(builder, "air pressure", probe.AirPressure, "Pa")
		builder.WriteString(".\n")
	}
}

func writeMetric(builder *strings.Builder, label string, value *float64, unit string) {
	if value == nil {
		return
	}
	builder.WriteString(fmt.Sprintf(", %s %.1f%s", label, *value, unit))
}

type mistralConversationRequest struct {
	AgentID      string                 `json:"agent_id"`
	AgentVersion int                    `json:"agent_version"`
	Inputs       []mistralInput         `json:"inputs"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

type mistralInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralConversationResponse struct {
	Outputs []mistralOutput `json:"outputs"`
	Output  string          `json:"output"`
}

type mistralOutput struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (p *MistralProvider) Send(ctx context.Context, userMessage string, contextBlock string) (string, error) {
	if p.apiKey == "" {
		return "", errors.New("MISTRAL_API_KEY is not configured")
	}
	if p.agentID == "" {
		return "", errors.New("MISTRAL_AGENT_ID is not configured")
	}

	payload := mistralConversationRequest{
		AgentID:      p.agentID,
		AgentVersion: p.agentVersion,
		Inputs: []mistralInput{
			{
				Role:    "user",
				Content: strings.TrimSpace(contextBlock) + "\n\nUser question:\n" + userMessage,
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal mistral request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/conversations", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create mistral request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send mistral request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read mistral response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("mistral returned status %d", resp.StatusCode)
	}

	reply, err := extractMistralReply(respBody)
	if err != nil {
		return "", err
	}
	return reply, nil
}

func extractMistralReply(body []byte) (string, error) {
	var payload mistralConversationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode mistral response: %w", err)
	}
	if strings.TrimSpace(payload.Output) != "" {
		return strings.TrimSpace(payload.Output), nil
	}
	for _, output := range payload.Outputs {
		if output.Role != "assistant" && output.Role != "" {
			continue
		}
		if reply := decodeMistralContent(output.Content); reply != "" {
			return reply, nil
		}
	}
	return "", errors.New("mistral response did not contain assistant content")
}

func decodeMistralContent(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var chunks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &chunks); err == nil {
		parts := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			if chunk.Text != "" {
				parts = append(parts, chunk.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}
