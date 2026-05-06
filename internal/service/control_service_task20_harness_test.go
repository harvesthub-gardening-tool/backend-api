package service_test

import (
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	controlv1 "github.com/harvesthub-gardening-tool/protos-go/control/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	auth "harvest-hub/api/internal/auth"
	"harvest-hub/api/internal/service"
)

func TestControlService_Task20HarnessLifecycleAndFailureMatrix(t *testing.T) {
	t.Run("create poll ack sent ack succeeded status lifecycle", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "task20-lifecycle@example.com", "node-task20-lifecycle")
		svc := service.NewControlService(db)

		createResp, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-lifecycle-idem",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-lifecycle",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1500,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)
		require.NotNil(t, createResp.Msg.Command)

		pullResp, err := svc.PullPendingMotorCommands(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		require.Len(t, pullResp.Msg.Commands, 1)
		assert.Equal(t, createResp.Msg.Command.CommandId, pullResp.Msg.Commands[0].CommandId)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_LEASED_TO_HUB, pullResp.Msg.Commands[0].Status)

		_, err = svc.AckMotorCommandEvent(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     createResp.Msg.Command.CommandId,
			NodeId:        "node-task20-lifecycle",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "delivered to probe simulation",
		}))
		require.NoError(t, err)

		ackResp, err := svc.AckMotorCommandEvent(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     createResp.Msg.Command.CommandId,
			NodeId:        "node-task20-lifecycle",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE,
			ReasonMessage: "motor completed simulation",
		}))
		require.NoError(t, err)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED, ackResp.Msg.Command.Status)

		statusResp, err := svc.GetMotorCommandStatus(controlUserCtx(user.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: createResp.Msg.Command.CommandId,
		}))
		require.NoError(t, err)
		require.NotNil(t, statusResp.Msg.Command)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED, statusResp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE, statusResp.Msg.Command.ReasonCode)

		events := requireCommandEvents(t, db, createResp.Msg.Command.CommandId)
		require.Len(t, events, 4)
		assert.Equal(t, []string{"queued", "leased_to_hub", "sent_to_probe", "succeeded"}, []string{events[0].NewStatus, events[1].NewStatus, events[2].NewStatus, events[3].NewStatus})
	})

	t.Run("unauthorized create is rejected", func(t *testing.T) {
		db := setupControlServiceDB(t)
		_, hub, _ := createOwnedNodeFixture(t, db, "task20-owner@example.com", "node-task20-unauthorized")
		outsider := auth.User{Email: "task20-outsider@example.com", PasswordHash: "hash"}
		require.NoError(t, db.Create(&outsider).Error)
		svc := service.NewControlService(db)

		_, err := svc.CreateMotorCommand(controlUserCtx(outsider.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-unauthorized-idem",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-unauthorized",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     900,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		var commandCount int64
		require.NoError(t, db.Model(&auth.MotorCommand{}).Where("user_id = ?", outsider.ID).Count(&commandCount).Error)
		assert.Equal(t, int64(0), commandCount)
	})

	t.Run("duplicate idempotency replay returns existing command", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "task20-duplicate@example.com", "node-task20-duplicate")
		svc := service.NewControlService(db)

		request := &controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-duplicate-idem",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-duplicate",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     800,
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
	})

	t.Run("active command duplicate is rejected", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "task20-active-duplicate@example.com", "node-task20-active")
		svc := service.NewControlService(db)

		_, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-active-first",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-active",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     700,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)

		_, err = svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-active-second",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-active",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     750,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("expired command is not dispatched when offline hub misses poll window", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "task20-offline-hub@example.com", "node-task20-offline-hub")
		svc := service.NewControlService(db)

		createResp, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-offline-hub-idem",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-offline-hub",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1200,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)

		expiredAt := time.Now().Add(-1 * time.Second)
		require.NoError(t, db.Model(&auth.MotorCommand{}).
			Where("command_id = ?", createResp.Msg.Command.CommandId).
			Update("expires_at", expiredAt).Error)

		pullResp, err := svc.PullPendingMotorCommands(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)
		assert.Empty(t, pullResp.Msg.Commands)

		statusResp, err := svc.GetMotorCommandStatus(controlUserCtx(user.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: createResp.Msg.Command.CommandId,
		}))
		require.NoError(t, err)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXPIRED, statusResp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_EXPIRED, statusResp.Msg.Command.ReasonCode)

		events := requireCommandEvents(t, db, createResp.Msg.Command.CommandId)
		require.Len(t, events, 2)
		assert.Equal(t, []string{"queued", "expired"}, []string{events[0].NewStatus, events[1].NewStatus})
	})

	t.Run("offline probe simulation transitions failed status", func(t *testing.T) {
		db := setupControlServiceDB(t)
		user, hub, _ := createOwnedNodeFixture(t, db, "task20-offline-probe@example.com", "node-task20-offline-probe")
		svc := service.NewControlService(db)

		createResp, err := svc.CreateMotorCommand(controlUserCtx(user.ID), connect.NewRequest(&controlv1.CreateMotorCommandRequest{
			IdempotencyKey: "task20-offline-probe-idem",
			HubId:          fmt.Sprint(hub.ID),
			NodeId:         "node-task20-offline-probe",
			Action:         controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION,
			DurationMs:     1000,
			ExpiresAt:      time.Now().Add(20 * time.Second).UnixMilli(),
		}))
		require.NoError(t, err)

		_, err = svc.PullPendingMotorCommands(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.PullPendingMotorCommandsRequest{
			MaxCommands:     1,
			LeaseDurationMs: 15000,
		}))
		require.NoError(t, err)

		ackResp, err := svc.AckMotorCommandEvent(controlHubCtx(user.ID, hub.ID), connect.NewRequest(&controlv1.AckMotorCommandEventRequest{
			CommandId:     createResp.Msg.Command.CommandId,
			NodeId:        "node-task20-offline-probe",
			Status:        controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED,
			ReasonCode:    controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_PROBE_UNREACHABLE,
			ReasonMessage: "probe offline simulation",
		}))
		require.NoError(t, err)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED, ackResp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_PROBE_UNREACHABLE, ackResp.Msg.Command.ReasonCode)

		statusResp, err := svc.GetMotorCommandStatus(controlUserCtx(user.ID), connect.NewRequest(&controlv1.GetMotorCommandStatusRequest{
			CommandId: createResp.Msg.Command.CommandId,
		}))
		require.NoError(t, err)
		assert.Equal(t, controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED, statusResp.Msg.Command.Status)
		assert.Equal(t, controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_PROBE_UNREACHABLE, statusResp.Msg.Command.ReasonCode)
		assert.Equal(t, "probe offline simulation", statusResp.Msg.Command.ReasonMessage)

		events := requireCommandEvents(t, db, createResp.Msg.Command.CommandId)
		require.Len(t, events, 3)
		assert.Equal(t, []string{"queued", "leased_to_hub", "failed"}, []string{events[0].NewStatus, events[1].NewStatus, events[2].NewStatus})
	})
}
