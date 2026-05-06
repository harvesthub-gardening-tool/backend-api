package service_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	controlv1 "github.com/harvesthub-gardening-tool/protos-go/control/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
	"harvest-hub/api/internal/service"
)

func setupControlServiceDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&auth.User{},
		&auth.HubToken{},
		&auth.Hub{},
		&auth.SensorNode{},
		&auth.MotorCommand{},
		&auth.MotorCommandEvent{},
	))

	return db
}

func controlUserCtx(userID uint) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID:   fmt.Sprint(userID),
		Username: "user@example.com",
	})
}

func controlHubCtx(userID, hubID uint) context.Context {
	return authctx.SetAuthInfo(context.Background(), &authctx.AuthInfo{
		UserID: fmt.Sprint(userID),
		HubID:  fmt.Sprint(hubID),
	})
}

func createOwnedNodeFixture(t *testing.T, db *gorm.DB, email, nodeID string) (auth.User, auth.Hub, auth.SensorNode) {
	t.Helper()

	user := auth.User{Email: email, PasswordHash: "hash"}
	require.NoError(t, db.Create(&user).Error)

	hub := auth.Hub{UserID: user.ID, Name: "Fixture Hub"}
	require.NoError(t, db.Create(&hub).Error)

	node := auth.SensorNode{NodeID: nodeID, HubID: &hub.ID}
	require.NoError(t, db.Create(&node).Error)

	return user, hub, node
}

func requireSingleCommandEvent(t *testing.T, db *gorm.DB, commandID string) auth.MotorCommandEvent {
	t.Helper()

	var events []auth.MotorCommandEvent
	require.NoError(t, db.Where("command_id = ?", commandID).Find(&events).Error)
	require.Len(t, events, 1)
	return events[0]
}

func requireCommandByExternalID(t *testing.T, db *gorm.DB, commandID string) auth.MotorCommand {
	t.Helper()

	var command auth.MotorCommand
	require.NoError(t, db.Where("command_id = ?", commandID).First(&command).Error)
	return command
}

func requireCommandEvents(t *testing.T, db *gorm.DB, commandID string) []auth.MotorCommandEvent {
	t.Helper()

	var events []auth.MotorCommandEvent
	require.NoError(t, db.Where("command_id = ?", commandID).Order("id asc").Find(&events).Error)
	return events
}

func TestControlService_CreateMotorCommand(t *testing.T) {
	t.Run("authorized create stores command and initial event", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-create@example.com", "node-create-1")
		svc := service.NewControlService(db)

		resp, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-create-1",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-create-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1200,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Command)

		assert.NotEmpty(t, resp.Msg.Command.CommandId)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_QUEUED, resp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE, resp.Msg.Command.ReasonCode)
		assert.Equal(t, fmt.Sprint(user.ID), resp.Msg.Command.RequestedByUserId)

		var commands []auth.MotorCommand
		require.NoError(t, db.Find(&commands).Error)
		require.Len(t, commands, 1)
		assert.Equal(t, resp.Msg.Command.CommandId, commands[0].CommandID)
		assert.Equal(t, user.ID, commands[0].UserID)
		assert.Equal(t, hub.ID, commands[0].HubID)
		assert.Equal(t, "node-create-1", commands[0].NodeID)
		assert.Equal(t, "run_for_duration", commands[0].Action)
		assert.Equal(t, int64(1200), commands[0].DurationMS)
		assert.Equal(t, "queued", commands[0].Status)

		event := requireSingleCommandEvent(t, db, commands[0].CommandID)
		assert.Equal(t, "user", event.ActorType)
		assert.Equal(t, fmt.Sprint(user.ID), event.ActorID)
		assert.Equal(t, "queued", event.NewStatus)
		assert.Equal(t, "none", event.ReasonCode)
	})

	t.Run("duplicate idempotency returns existing command without creating a new row", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-dup@example.com", "node-dup-1")
		svc := service.NewControlService(db)

		request := &controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-dup-1",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-dup-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     900,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}

		first, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(request))
		require.NoError(t, err)
		second, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(request))
		require.NoError(t, err)

		assert.Equal(t, first.Msg.Command.CommandId, second.Msg.Command.CommandId)

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Count(&commandCount).Error)
		assert.Equal(t, int64(1), commandCount)

		var eventCount int64
		require.NoError(t, db.Model(&auth.MotorCommandEvent{}).Count(&eventCount).Error)
		assert.Equal(t, int64(1), eventCount)
	})

	t.Run("different idempotency key is rejected while an active command exists", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-active-conflict@example.com", "node-active-1")
		activeCommand := auth.MotorCommand{
			CommandID:      "cmd-active-conflict",
			UserID:         user.ID,
			HubID:          hub.ID,
			NodeID:         "node-active-1",
			Action:         "run_for_duration",
			DurationMS:     800,
			Status:         "queued",
			IdempotencyKey: "idem-active-existing",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&activeCommand).Error)
		svc := service.NewControlService(db)

		_, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-active-new",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-active-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1200,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Count(&commandCount).Error)
		assert.Equal(t, int64(1), commandCount)

		var eventCount int64
		require.NoError(t, db.Model(&auth.MotorCommandEvent{}).Count(&eventCount).Error)
		assert.Equal(t, int64(0), eventCount)
	})

	t.Run("terminal command does not block a new command", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-terminal-ok@example.com", "node-terminal-1")
		terminalStatuses := []string{"succeeded", "failed", "expired", "cancelled"}

		for i, status := range terminalStatuses {
			command := auth.MotorCommand{
				CommandID:      fmt.Sprintf("cmd-terminal-%d", i),
				UserID:         user.ID,
				HubID:          hub.ID,
				NodeID:         "node-terminal-1",
				Action:         "run_for_duration",
				DurationMS:     500,
				Status:         status,
				IdempotencyKey: fmt.Sprintf("idem-terminal-existing-%d", i),
				ReasonCode:     "none",
				ExpiresAt:      time.Now().Add(20 * time.Second),
			}
			require.NoError(t, db.Create(&command).Error)
		}

		svc := service.NewControlService(db)
		resp, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-terminal-new",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-terminal-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1100,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Command)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_QUEUED, resp.Msg.Command.Status)

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Count(&commandCount).Error)
		assert.Equal(t, int64(len(terminalStatuses)+1), commandCount)

		var eventCount int64
		require.NoError(t, db.Model(&auth.MotorCommandEvent{}).Count(&eventCount).Error)
		assert.Equal(t, int64(1), eventCount)
	})

	t.Run("unauthorized target creates no command or event", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-owner@example.com", "node-owner-1")
		outsider := auth.User{Email: "control-outsider@example.com", PasswordHash: "hash"}
		require.NoError(t, db.Create(&outsider).Error)
		svc := service.NewControlService(db)

		_, err := svc.CreateMotorCommand(controlUserCtx(outsider.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-unauthorized-1",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-owner-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     700,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Where("user_id = ?", outsider.ID).Count(&commandCount).Error)
		assert.Equal(t, int64(0), commandCount)

		var eventCount int64
		require.NoError(t, db.Model(&auth.MotorCommandEvent{}).Count(&eventCount).Error)
		assert.Equal(t, int64(0), eventCount)

		assert.NotZero(t, owner.ID)
	})

	t.Run("invalid duration and action create no command or event", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-invalid@example.com", "node-invalid-1")
		svc := service.NewControlService(db)

		cases := []struct {
			name string
			req  *controlv1.CreateMotorCommandRequest
		}{
			{
				name: "duration above max",
				req: &controlv1.CreateMotorCommandRequest{
					IdempotencyKey: "idem-invalid-1",
					HubId:          fmt.Sprint(hub.ID),
					NodeId:         "node-invalid-1",
					Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
					DurationMs:     5001,
					ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
				},
			},
			{
				name: "unsupported action",
				req: &controlv1.CreateMotorCommandRequest{
					IdempotencyKey: "idem-invalid-2",
					HubId:          fmt.Sprint(hub.ID),
					NodeId:         "node-invalid-1",
					Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_UNSPECIFIED,
					DurationMs:     100,
					ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
				},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(tc.req))
				require.Error(t, err)
				assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			})
		}

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Count(&commandCount).Error)
		assert.Equal(t, int64(0), commandCount)

		var eventCount int64
		require.NoError(t, db.Model(&auth.MotorCommandEvent{}).Count(&eventCount).Error)
		assert.Equal(t, int64(0), eventCount)
	})

	t.Run("invalid expiry window create no command or event", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-expiry-invalid@example.com", "node-expiry-invalid-1")
		svc := service.NewControlService(db)

		cases := []struct {
			name      string
			expiresAt int64
		}{
			{
				name:      "expiry below minimum ttl",
				expiresAt: time.Now().Add(2 * time.Second).UnixMilli(),
			},
			{
				name:      "expiry above maximum ttl",
				expiresAt: time.Now().Add(45 * time.Second).UnixMilli(),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
					IdempotencyKey: fmt.Sprintf("idem-expiry-%s", tc.name),
					HubId:          fmt.Sprint(hub.ID),
					NodeId:         "node-expiry-invalid-1",
					Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
					DurationMs:     700,
					ExpiresAt:      tc.expiresAt,
				}))
				require.Error(t, err)
				assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			})
		}

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Count(&commandCount).Error)
		assert.Equal(t, int64(0), commandCount)

		var eventCount int64
		require.NoError(t, db.Model(&auth.MotorCommandEvent{}).Count(&eventCount).Error)
		assert.Equal(t, int64(0), eventCount)
	})

	t.Run("rate limiting rejects burst for same user and node", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-rate-limit@example.com", "node-rate-limit-1")
		svc := service.NewControlService(db)

		for i := 0; i < 5; i++ {
			command := auth.MotorCommand{
				CommandID:      fmt.Sprintf("cmd-rate-limit-%d", i),
				UserID:         user.ID,
				HubID:          hub.ID,
				NodeID:         "node-rate-limit-1",
				Action:         "run_for_duration",
				DurationMS:     500,
				Status:         "succeeded",
				IdempotencyKey: fmt.Sprintf("idem-rate-limit-%d", i),
				ReasonCode:     "none",
				CreatedAt:      time.Now().Add(-5 * time.Second),
				UpdatedAt:      time.Now().Add(-5 * time.Second),
				ExpiresAt:      time.Now().Add(20 * time.Second),
			}
			require.NoError(t, db.Create(&command).Error)
		}

		_, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-rate-limit-new",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-rate-limit-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     700,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Count(&commandCount).Error)
		assert.Equal(t, int64(5), commandCount)
	})

	t.Run("stale leased command is requeued and still blocks a new create", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-stale-create@example.com", "node-stale-create-1")
		oldLeaseTime := time.Now().Add(-20 * time.Second)
		oldLeaseExpiry := time.Now().Add(-1 * time.Second)
		staleLeased := auth.MotorCommand{
			CommandID:      "cmd-stale-create-existing",
			UserID:         user.ID,
			HubID:          hub.ID,
			NodeID:         "node-stale-create-1",
			Action:         "run_for_duration",
			DurationMS:     800,
			Status:         "leased_to_hub",
			IdempotencyKey: "idem-stale-create-existing",
			ReasonCode:     "none",
			LeaseToken:     ptr("lease-stale-create"),
			LeasedAt:       &oldLeaseTime,
			LeaseExpiresAt: &oldLeaseExpiry,
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&staleLeased).Error)
		svc := service.NewControlService(db)

		_, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-stale-create-new",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-stale-create-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1200,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		stored := requireCommandByExternalID(t, db, "cmd-stale-create-existing")
		assert.Equal(t, "queued", stored.Status)
		assert.Nil(t, stored.LeaseToken)
		assert.Nil(t, stored.LeaseExpiresAt)

		events := requireCommandEvents(t, db, "cmd-stale-create-existing")
		require.Len(t, events, 1)
		assert.Equal(t, "system", events[0].ActorType)
		assert.Equal(t, "leased_to_hub", events[0].PreviousStatus)
		assert.Equal(t, "queued", events[0].NewStatus)
		assert.Equal(t, "lease expired; command requeued", events[0].Message)
	})

	t.Run("hub tokens cannot create motor commands", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-hubtoken@example.com", "node-hubtoken-1")
		svc := service.NewControlService(db)

		_, err := svc.CreateMotorCommand(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-hub-token-1",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-hubtoken-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     600,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}

func TestControlService_GetMotorCommandStatus(t *testing.T) {
	t.Run("returns status for visible command", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-status@example.com", "node-status-1")
		command := auth.MotorCommand{
			CommandID:      "cmd-status-visible",
			UserID:         user.ID,
			HubID:          hub.ID,
			NodeID:         "node-status-1",
			Action:         "run_for_duration",
			DurationMS:     1500,
			Status:         "failed",
			IdempotencyKey: "idem-status-visible",
			ReasonCode:     "ble_write_failed",
			ReasonMessage:  "probe offline",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)
		svc := service.NewControlService(db)

		resp, err := svc.GetMotorCommandStatus(controlUserCtx(user.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: "cmd-status-visible",
		}))
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Command)
		assert.Equal(t, "cmd-status-visible", resp.Msg.Command.CommandId)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED, resp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_BLE_WRITE_FAILED, resp.Msg.Command.ReasonCode)
		assert.Equal(t, "probe offline", resp.Msg.Command.ReasonMessage)
	})

	t.Run("expired queued command is reconciled on access with system audit event", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-status-expired@example.com", "node-status-expired")
		command := auth.MotorCommand{
			CommandID:      "cmd-status-expired",
			UserID:         user.ID,
			HubID:          hub.ID,
			NodeID:         "node-status-expired",
			Action:         "run_for_duration",
			DurationMS:     400,
			Status:         "queued",
			IdempotencyKey: "idem-status-expired",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(-1 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)
		svc := service.NewControlService(db)

		resp, err := svc.GetMotorCommandStatus(controlUserCtx(user.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: "cmd-status-expired",
		}))
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Command)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXPIRED, resp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_EXPIRED, resp.Msg.Command.ReasonCode)
		assert.Equal(t, "command expired before delivery", resp.Msg.Command.ReasonMessage)

		stored := requireCommandByExternalID(t, db, "cmd-status-expired")
		assert.Equal(t, "expired", stored.Status)
		assert.Equal(t, "expired", stored.ReasonCode)
		assert.Equal(t, "command expired before delivery", stored.ReasonMessage)
		assert.NotNil(t, stored.CompletedAt)

		events := requireCommandEvents(t, db, "cmd-status-expired")
		require.Len(t, events, 1)
		assert.Equal(t, "system", events[0].ActorType)
		assert.Equal(t, "system", events[0].ActorID)
		assert.Equal(t, "queued", events[0].PreviousStatus)
		assert.Equal(t, "expired", events[0].NewStatus)
		assert.Equal(t, "expired", events[0].ReasonCode)
		assert.Equal(t, "command expired before delivery", events[0].ReasonMessage)
	})

	t.Run("hidden command is not visible to another user", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-owner-status@example.com", "node-status-2")
		outsider := auth.User{Email: "control-outsider-status@example.com", PasswordHash: "hash"}
		require.NoError(t, db.Create(&outsider).Error)

		command := auth.MotorCommand{
			CommandID:      "cmd-status-hidden",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-status-2",
			Action:         "run_for_duration",
			DurationMS:     900,
			Status:         "queued",
			IdempotencyKey: "idem-status-hidden",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)
		svc := service.NewControlService(db)

		_, err := svc.GetMotorCommandStatus(controlUserCtx(outsider.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: "cmd-status-hidden",
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("hub tokens cannot read user-facing command status", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-status-hub@example.com", "node-status-3")
		svc := service.NewControlService(db)

		_, err := svc.GetMotorCommandStatus(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: "cmd-any",
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("hidden expired command remains invisible and does not leak audit event to another user", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-owner-status-expired@example.com", "node-status-foreign-expired")
		outsider := auth.User{Email: "control-outsider-status-expired@example.com", PasswordHash: "hash"}
		require.NoError(t, db.Create(&outsider).Error)

		command := auth.MotorCommand{
			CommandID:      "cmd-status-hidden-expired",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-status-foreign-expired",
			Action:         "run_for_duration",
			DurationMS:     500,
			Status:         "queued",
			IdempotencyKey: "idem-status-hidden-expired",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(-1 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)
		svc := service.NewControlService(db)

		_, err := svc.GetMotorCommandStatus(controlUserCtx(outsider.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: "cmd-status-hidden-expired",
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		stored := requireCommandByExternalID(t, db, "cmd-status-hidden-expired")
		assert.Equal(t, "queued", stored.Status)
		assert.Nil(t, stored.CompletedAt)
		assert.Empty(t, requireCommandEvents(t, db, "cmd-status-hidden-expired"))
	})

	t.Run("missing command id is rejected", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, _, _ := createOwnedNodeFixture(t, db, "control-status-missing@example.com", "node-status-4")
		svc := service.NewControlService(db)

		_, err := svc.GetMotorCommandStatus(controlUserCtx(user.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestControlService_PullPendingMotorCommands(t *testing.T) {
	t.Run("hub poll leases only its own pending command and ignores request hub id", func(t *testing.T) {
		db := setupControlServiceDB(t)
		ownerA, hubA, _ := createOwnedNodeFixture(t, db, "control-poll-a@example.com", "node-poll-a")
		_, hubB, _ := createOwnedNodeFixture(t, db, "control-poll-b@example.com", "node-poll-b")

		commandA := auth.MotorCommand{
			CommandID:      "cmd-poll-a",
			UserID:         ownerA.ID,
			HubID:          hubA.ID,
			NodeID:         "node-poll-a",
			Action:         "run_for_duration",
			DurationMS:     1200,
			Status:         "queued",
			IdempotencyKey: "idem-poll-a",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		commandB := auth.MotorCommand{
			CommandID:      "cmd-poll-b",
			UserID:         ownerA.ID,
			HubID:          hubB.ID,
			NodeID:         "node-poll-b",
			Action:         "run_for_duration",
			DurationMS:     900,
			Status:         "queued",
			IdempotencyKey: "idem-poll-b",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&commandA).Error)
		require.NoError(t, db.Create(&commandB).Error)

		svc := service.NewControlService(db)
		resp, err := svc.PullPendingMotorCommands(controlHubCtx(ownerA.ID, hubA.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			HubId:           fmt.Sprint(hubB.ID),
			MaxCommands:     5,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		require.Len(t, resp.Msg.Commands, 1)
		assert.Equal(t, "cmd-poll-a", resp.Msg.Commands[0].CommandId)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_LEASED_TO_HUB, resp.Msg.Commands[0].Status)
		assert.NotZero(t, resp.Msg.Commands[0].LeaseExpiresAt)

		storedA := requireCommandByExternalID(t, db, "cmd-poll-a")
		assert.Equal(t, "leased_to_hub", storedA.Status)
		assert.NotNil(t, storedA.LeaseToken)
		assert.NotNil(t, storedA.LeasedAt)
		assert.NotNil(t, storedA.LeaseExpiresAt)

		storedB := requireCommandByExternalID(t, db, "cmd-poll-b")
		assert.Equal(t, "queued", storedB.Status)
		assert.Nil(t, storedB.LeaseToken)

		events := requireCommandEvents(t, db, "cmd-poll-a")
		require.Len(t, events, 1)
		assert.Equal(t, "hub", events[0].ActorType)
		assert.Equal(t, fmt.Sprint(hubA.ID), events[0].ActorID)
		assert.Equal(t, "queued", events[0].PreviousStatus)
		assert.Equal(t, "leased_to_hub", events[0].NewStatus)
	})

	t.Run("user jwt cannot poll hub queue", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-poll-user@example.com", "node-poll-user")
		svc := service.NewControlService(db)

		_, err := svc.PullPendingMotorCommands(controlUserCtx(user.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			HubId:           fmt.Sprint(hub.ID),
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("duplicate poll during active lease returns no duplicate", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-poll-dup@example.com", "node-poll-dup")
		command := auth.MotorCommand{
			CommandID:      "cmd-poll-dup",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-poll-dup",
			Action:         "run_for_duration",
			DurationMS:     1000,
			Status:         "queued",
			IdempotencyKey: "idem-poll-dup",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)

		svc := service.NewControlService(db)
		first, err := svc.PullPendingMotorCommands(controlHubCtx(owner.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		require.Len(t, first.Msg.Commands, 1)

		second, err := svc.PullPendingMotorCommands(controlHubCtx(owner.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		assert.Empty(t, second.Msg.Commands)

		events := requireCommandEvents(t, db, "cmd-poll-dup")
		require.Len(t, events, 1)
		assert.Equal(t, "leased_to_hub", events[0].NewStatus)
	})

	t.Run("expired queued and stale leased commands are cleaned before polling", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-poll-expiry@example.com", "node-poll-expiry")
		now := time.Now()
		oldLeaseTime := now.Add(-20 * time.Second)
		oldLeaseExpiry := now.Add(-5 * time.Second)

		expiredQueued := auth.MotorCommand{
			CommandID:      "cmd-expired-queued",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-expired-queued",
			Action:         "run_for_duration",
			DurationMS:     500,
			Status:         "queued",
			IdempotencyKey: "idem-expired-queued",
			ReasonCode:     "none",
			ExpiresAt:      now.Add(-1 * time.Second),
		}
		expiredLeased := auth.MotorCommand{
			CommandID:      "cmd-expired-leased",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-expired-leased",
			Action:         "run_for_duration",
			DurationMS:     600,
			Status:         "leased_to_hub",
			IdempotencyKey: "idem-expired-leased",
			ReasonCode:     "none",
			LeaseToken:     ptr("lease-expired"),
			LeasedAt:       &oldLeaseTime,
			LeaseExpiresAt: &oldLeaseExpiry,
			ExpiresAt:      now.Add(-500 * time.Millisecond),
		}
		staleLeased := auth.MotorCommand{
			CommandID:      "cmd-stale-leased",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-stale-leased",
			Action:         "run_for_duration",
			DurationMS:     700,
			Status:         "leased_to_hub",
			IdempotencyKey: "idem-stale-leased",
			ReasonCode:     "none",
			LeaseToken:     ptr("lease-stale"),
			LeasedAt:       &oldLeaseTime,
			LeaseExpiresAt: &oldLeaseExpiry,
			ExpiresAt:      now.Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&expiredQueued).Error)
		require.NoError(t, db.Create(&expiredLeased).Error)
		require.NoError(t, db.Create(&staleLeased).Error)

		svc := service.NewControlService(db)
		resp, err := svc.PullPendingMotorCommands(controlHubCtx(owner.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     5,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		require.Len(t, resp.Msg.Commands, 1)
		assert.Equal(t, "cmd-stale-leased", resp.Msg.Commands[0].CommandId)

		storedExpiredQueued := requireCommandByExternalID(t, db, "cmd-expired-queued")
		assert.Equal(t, "expired", storedExpiredQueued.Status)
		assert.Equal(t, "expired", storedExpiredQueued.ReasonCode)
		assert.NotNil(t, storedExpiredQueued.CompletedAt)

		storedExpiredLeased := requireCommandByExternalID(t, db, "cmd-expired-leased")
		assert.Equal(t, "expired", storedExpiredLeased.Status)
		assert.Equal(t, "expired", storedExpiredLeased.ReasonCode)
		assert.Nil(t, storedExpiredLeased.LeaseToken)
		assert.NotNil(t, storedExpiredLeased.CompletedAt)

		storedStaleLeased := requireCommandByExternalID(t, db, "cmd-stale-leased")
		assert.Equal(t, "leased_to_hub", storedStaleLeased.Status)
		assert.NotNil(t, storedStaleLeased.LeaseToken)
		assert.NotNil(t, storedStaleLeased.LeaseExpiresAt)

		staleEvents := requireCommandEvents(t, db, "cmd-stale-leased")
		require.Len(t, staleEvents, 2)
		assert.Equal(t, "leased_to_hub", staleEvents[0].PreviousStatus)
		assert.Equal(t, "queued", staleEvents[0].NewStatus)
		assert.Equal(t, "queued", staleEvents[1].PreviousStatus)
		assert.Equal(t, "leased_to_hub", staleEvents[1].NewStatus)
	})
}

func TestControlService_AckMotorCommandEvent(t *testing.T) {
	t.Run("hub can ack valid transitions through terminal success", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-ack-valid@example.com", "node-ack-valid")
		leaseToken := "lease-valid"
		leasedAt := time.Now().Add(-2 * time.Second)
		leaseExpiresAt := time.Now().Add(15 * time.Second)
		command := auth.MotorCommand{
			CommandID:      "cmd-ack-valid",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-ack-valid",
			Action:         "run_for_duration",
			DurationMS:     1400,
			Status:         "leased_to_hub",
			IdempotencyKey: "idem-ack-valid",
			ReasonCode:     "none",
			LeaseToken:     &leaseToken,
			LeasedAt:       &leasedAt,
			LeaseExpiresAt: &leaseExpiresAt,
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)

		svc := service.NewControlService(db)
		statuses := []controlv1.MotorCommandStatus{
			controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE,
			controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXECUTING,
			controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED,
		}

		for _, status := range statuses {
			resp, err := svc.AckMotorCommandEvent(controlHubCtx(owner.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
				CommandId:     "cmd-ack-valid",
				HubId:         "999999",
				NodeId:        "node-ack-valid",
				Status:        status,
				ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
				ReasonMessage: status.String(),
			}))
			require.NoError(t, err)
			assert.Equal(t, status, resp.Msg.Command.Status)
		}

		stored := requireCommandByExternalID(t, db, "cmd-ack-valid")
		assert.Equal(t, "succeeded", stored.Status)
		assert.Equal(t, "none", stored.ReasonCode)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED, motorStatusToProtoForTest(stored.Status))
		assert.NotNil(t, stored.CompletedAt)

		events := requireCommandEvents(t, db, "cmd-ack-valid")
		require.Len(t, events, 3)
		assert.Equal(t, "hub", events[0].ActorType)
		assert.Equal(t, "sent_to_probe", events[0].NewStatus)
		assert.Equal(t, "executing", events[1].NewStatus)
		assert.Equal(t, "succeeded", events[2].NewStatus)
	})

	t.Run("create poll ack appends canonical transition history in order", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "control-lifecycle@example.com", "node-lifecycle-1")
		svc := service.NewControlService(db)

		createResp, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "idem-lifecycle-1",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-lifecycle-1",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1200,
			ExpiresAt:      time.Now().Add(30 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)

		pullResp, err := svc.PullPendingMotorCommands(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		require.Len(t, pullResp.Msg.Commands, 1)
		assert.Equal(t, createResp.Msg.Command.CommandId, pullResp.Msg.Commands[0].CommandId)

		_, err = svc.AckMotorCommandEvent(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     createResp.Msg.Command.CommandId,
			NodeId:        "node-lifecycle-1",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "delivered to probe",
		}))
		require.NoError(t, err)

		_, err = svc.AckMotorCommandEvent(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     createResp.Msg.Command.CommandId,
			NodeId:        "node-lifecycle-1",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXECUTING,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "motor started",
		}))
		require.NoError(t, err)

		ackResp, err := svc.AckMotorCommandEvent(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     createResp.Msg.Command.CommandId,
			NodeId:        "node-lifecycle-1",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "completed",
		}))
		require.NoError(t, err)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED, ackResp.Msg.Command.Status)

		events := requireCommandEvents(t, db, createResp.Msg.Command.CommandId)
		require.Len(t, events, 5)
		assert.Equal(t, "user", events[0].ActorType)
		assert.Equal(t, fmt.Sprint(user.ID), events[0].ActorID)
		assert.Equal(t, "", events[0].PreviousStatus)
		assert.Equal(t, "queued", events[0].NewStatus)
		assert.Equal(t, "none", events[0].ReasonCode)
		assert.Equal(t, "command queued", events[0].Message)

		assert.Equal(t, "hub", events[1].ActorType)
		assert.Equal(t, fmt.Sprint(hub.ID), events[1].ActorID)
		assert.Equal(t, "queued", events[1].PreviousStatus)
		assert.Equal(t, "leased_to_hub", events[1].NewStatus)
		assert.Equal(t, "none", events[1].ReasonCode)

		assert.Equal(t, "leased_to_hub", events[2].PreviousStatus)
		assert.Equal(t, "sent_to_probe", events[2].NewStatus)
		assert.Equal(t, "none", events[2].ReasonCode)
		assert.Equal(t, "delivered to probe", events[2].ReasonMessage)

		assert.Equal(t, "sent_to_probe", events[3].PreviousStatus)
		assert.Equal(t, "executing", events[3].NewStatus)
		assert.Equal(t, "motor started", events[3].ReasonMessage)

		assert.Equal(t, "executing", events[4].PreviousStatus)
		assert.Equal(t, "succeeded", events[4].NewStatus)
		assert.Equal(t, "none", events[4].ReasonCode)
		assert.Equal(t, "completed", events[4].ReasonMessage)
	})

	t.Run("wrong hub cannot ack command", func(t *testing.T) {
		db := setupControlServiceDB(t)
		ownerA, hubA, _ := createOwnedNodeFixture(t, db, "control-ack-owner-a@example.com", "node-ack-owner-a")
		ownerB, hubB, _ := createOwnedNodeFixture(t, db, "control-ack-owner-b@example.com", "node-ack-owner-b")
		leaseToken := "lease-cross-hub"
		leasedAt := time.Now().Add(-2 * time.Second)
		leaseExpiresAt := time.Now().Add(10 * time.Second)
		command := auth.MotorCommand{
			CommandID:      "cmd-ack-cross-hub",
			UserID:         ownerA.ID,
			HubID:          hubA.ID,
			NodeID:         "node-ack-owner-a",
			Action:         "run_for_duration",
			DurationMS:     500,
			Status:         "leased_to_hub",
			IdempotencyKey: "idem-ack-cross-hub",
			ReasonCode:     "none",
			LeaseToken:     &leaseToken,
			LeasedAt:       &leasedAt,
			LeaseExpiresAt: &leaseExpiresAt,
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)

		svc := service.NewControlService(db)
		_, err := svc.AckMotorCommandEvent(controlHubCtx(ownerB.ID, hubB.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     "cmd-ack-cross-hub",
			NodeId:        "node-ack-owner-a",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "wrong hub",
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		stored := requireCommandByExternalID(t, db, "cmd-ack-cross-hub")
		assert.Equal(t, "leased_to_hub", stored.Status)
		assert.Len(t, requireCommandEvents(t, db, "cmd-ack-cross-hub"), 0)
	})

	t.Run("invalid ack transition is rejected", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-ack-invalid@example.com", "node-ack-invalid")
		command := auth.MotorCommand{
			CommandID:      "cmd-ack-invalid",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-ack-invalid",
			Action:         "run_for_duration",
			DurationMS:     900,
			Status:         "queued",
			IdempotencyKey: "idem-ack-invalid",
			ReasonCode:     "none",
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)

		svc := service.NewControlService(db)
		_, err := svc.AckMotorCommandEvent(controlHubCtx(owner.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     "cmd-ack-invalid",
			NodeId:        "node-ack-invalid",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "cannot skip lease",
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		stored := requireCommandByExternalID(t, db, "cmd-ack-invalid")
		assert.Equal(t, "queued", stored.Status)
		assert.Len(t, requireCommandEvents(t, db, "cmd-ack-invalid"), 0)
	})

	t.Run("duplicate ack retry returns existing command without new event", func(t *testing.T) {
		db := setupControlServiceDB(t)
		owner, hub, _ := createOwnedNodeFixture(t, db, "control-ack-duplicate@example.com", "node-ack-duplicate")
		leaseToken := "lease-duplicate"
		leasedAt := time.Now().Add(-2 * time.Second)
		leaseExpiresAt := time.Now().Add(15 * time.Second)
		command := auth.MotorCommand{
			CommandID:      "cmd-ack-duplicate",
			UserID:         owner.ID,
			HubID:          hub.ID,
			NodeID:         "node-ack-duplicate",
			Action:         "run_for_duration",
			DurationMS:     900,
			Status:         "sent_to_probe",
			IdempotencyKey: "idem-ack-duplicate",
			ReasonCode:     "none",
			ReasonMessage:  "already sent",
			LeaseToken:     &leaseToken,
			LeasedAt:       &leasedAt,
			LeaseExpiresAt: &leaseExpiresAt,
			ExpiresAt:      time.Now().Add(20 * time.Second),
		}
		require.NoError(t, db.Create(&command).Error)
		require.NoError(t, db.Create(&auth.MotorCommandEvent{
			MotorCommandID: command.ID,
			CommandID:      command.CommandID,
			ActorType:      "hub",
			ActorID:        fmt.Sprint(hub.ID),
			PreviousStatus: "leased_to_hub",
			NewStatus:      "sent_to_probe",
			ReasonCode:     "none",
			ReasonMessage:  "already sent",
			Message:        "command sent to probe",
			OccurredAt:     time.Now().Add(-1 * time.Second),
		}).Error)

		svc := service.NewControlService(db)
		resp, err := svc.AckMotorCommandEvent(controlHubCtx(owner.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     "cmd-ack-duplicate",
			NodeId:        "node-ack-duplicate",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "retry sent",
		}))
		require.NoError(t, err)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE, resp.Msg.Command.Status)
		assert.Equal(t, "already sent", resp.Msg.Command.ReasonMessage)

		stored := requireCommandByExternalID(t, db, "cmd-ack-duplicate")
		assert.Equal(t, "sent_to_probe", stored.Status)
		assert.Equal(t, "already sent", stored.ReasonMessage)

		events := requireCommandEvents(t, db, "cmd-ack-duplicate")
		require.Len(t, events, 1)
	})
}

func motorStatusToProtoForTest(status string) controlv1.MotorCommandStatus {
	switch status {
	case "queued":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_QUEUED
	case "leased_to_hub":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_LEASED_TO_HUB
	case "sent_to_probe":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE
	case "executing":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXECUTING
	case "succeeded":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED
	case "failed":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED
	case "expired":
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXPIRED
	default:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_UNSPECIFIED
	}
}
