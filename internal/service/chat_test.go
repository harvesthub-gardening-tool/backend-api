package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	sqlmock "github.com/DATA-DOG/go-sqlmock"
	chatv1 "github.com/harvesthub-gardening-tool/protos-go/chat/v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	authctx "harvest-hub/api/internal/auth/context"
)

type fakeChatProvider struct {
	reply        string
	err          error
	message      string
	contextBlock string
}

func newChatTestDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
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

func chatUserCtx(userID string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: "alice@example.com",
	})
}

func chatHubCtx(userID string, hubID string) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   userID,
		Username: "",
		HubID:    hubID,
	})
}

func (p *fakeChatProvider) Send(_ context.Context, userMessage string, contextBlock string) (string, error) {
	p.message = userMessage
	p.contextBlock = contextBlock
	return p.reply, p.err
}

func TestChatServiceSendMessage(t *testing.T) {
	t.Run("rejects hub tokens", func(t *testing.T) {
		db, mock := newChatTestDB(t)
		provider := &fakeChatProvider{reply: "ok"}
		svc := newChatServiceWithProvider(db, provider)

		_, err := svc.SendMessage(chatHubCtx("1", "42"), connect.NewRequest(&chatv1.SendMessageRequest{Message: "hello"}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("expected PermissionDenied, got %v", connect.CodeOf(err))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unexpected db call: %v", err)
		}
	})

	t.Run("rejects blank messages", func(t *testing.T) {
		db, mock := newChatTestDB(t)
		provider := &fakeChatProvider{reply: "ok"}
		svc := newChatServiceWithProvider(db, provider)

		_, err := svc.SendMessage(chatUserCtx("1"), connect.NewRequest(&chatv1.SendMessageRequest{Message: "   "}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("expected InvalidArgument, got %v", connect.CodeOf(err))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unexpected db call: %v", err)
		}
	})

	t.Run("injects plants and authorized latest probe readings", func(t *testing.T) {
		db, mock := newChatTestDB(t)
		provider := &fakeChatProvider{reply: "Arrosez légèrement ce soir."}
		svc := newChatServiceWithProvider(db, provider)
		readingTime := time.Date(2026, 5, 7, 8, 30, 0, 0, time.UTC)

		mock.ExpectQuery("FROM sensor_nodes sn").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{
				"node_id", "probe_name", "probe_location", "hub_name", "reading_time",
				"air_temperature", "air_pressure", "air_humidity", "soil_temperature", "soil_humidity",
			}).AddRow("node-1", "Probe terrasse", "Terrasse", "Hub maison", readingTime, 25.4, 101325.0, 44.0, 21.2, 31.5))

		res, err := svc.SendMessage(chatUserCtx("1"), connect.NewRequest(&chatv1.SendMessageRequest{
			Message: "Que dois-je faire ?",
			Plants: []*chatv1.ChatPlantContext{{
				Id:          "plant-1",
				Name:        "Tomate",
				Quantity:    2,
				ProbeNodeId: "node-1",
			}},
		}))
		if err != nil {
			t.Fatalf("SendMessage returned error: %v", err)
		}
		if res.Msg.Reply != "Arrosez légèrement ce soir." {
			t.Fatalf("unexpected reply %q", res.Msg.Reply)
		}
		if provider.message != "Que dois-je faire ?" {
			t.Fatalf("provider got message %q", provider.message)
		}
		for _, want := range []string{"Tomate", "node-1", "Probe terrasse", "soil humidity 31.5%"} {
			if !strings.Contains(provider.contextBlock, want) {
				t.Fatalf("context block missing %q:\n%s", want, provider.contextBlock)
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("maps provider failures to unavailable", func(t *testing.T) {
		db, mock := newChatTestDB(t)
		provider := &fakeChatProvider{err: errors.New("provider down")}
		svc := newChatServiceWithProvider(db, provider)

		mock.ExpectQuery("FROM sensor_nodes sn").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{
				"node_id", "probe_name", "probe_location", "hub_name", "reading_time",
				"air_temperature", "air_pressure", "air_humidity", "soil_temperature", "soil_humidity",
			}))

		_, err := svc.SendMessage(chatUserCtx("1"), connect.NewRequest(&chatv1.SendMessageRequest{Message: "hello"}))
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("expected Unavailable, got %v", connect.CodeOf(err))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

func TestExtractMistralReply(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "output string", body: `{"output":"Bonjour"}`, want: "Bonjour"},
		{name: "assistant content string", body: `{"outputs":[{"role":"assistant","content":"Salut"}]}`, want: "Salut"},
		{name: "assistant content chunks", body: `{"outputs":[{"role":"assistant","content":[{"type":"text","text":"A"},{"type":"text","text":"B"}]}]}`, want: "A\nB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractMistralReply([]byte(tt.body))
			if err != nil {
				t.Fatalf("extractMistralReply returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
