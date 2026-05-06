package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	controlv1 "github.com/harvesthub-gardening-tool/protos-go/control/v1"
	controlv1connect "github.com/harvesthub-gardening-tool/protos-go/control/v1/controlv1connect"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	auth "harvest-hub/api/internal/auth"
	authctx "harvest-hub/api/internal/auth/context"
)

const (
	maxMotorCommandDurationMS   = int32(5000)
	minMotorCommandTTL          = 5 * time.Second
	maxMotorCommandTTL          = 30 * time.Second
	defaultMotorCommandExpiry   = 30 * time.Second
	defaultMotorLeaseDuration   = 15 * time.Second
	maxPullMotorCommands        = int32(10)
	motorCommandRateLimitWindow = 30 * time.Second
	motorCommandRateLimitMax    = int64(5)

	dbMotorActionRunForDuration = "run_for_duration"
	dbMotorActionStop           = "stop"

	dbMotorStatusQueued    = "queued"
	dbMotorStatusLeased    = "leased_to_hub"
	dbMotorStatusSent      = "sent_to_probe"
	dbMotorStatusExecuting = "executing"
	dbMotorStatusSucceeded = "succeeded"
	dbMotorStatusFailed    = "failed"
	dbMotorStatusExpired   = "expired"
	dbMotorStatusCancelled = "cancelled"

	dbMotorReasonNone             = "none"
	dbMotorReasonUnauthorized     = "unauthorized"
	dbMotorReasonExpired          = "expired"
	dbMotorReasonDuplicate        = "duplicate"
	dbMotorReasonProbeUnreachable = "probe_unreachable"
	dbMotorReasonBLEWriteFailed   = "ble_write_failed"
	dbMotorReasonUARTTimeout      = "uart_timeout"
	dbMotorReasonUARTRejected     = "uart_rejected"
	dbMotorReasonSafetyLimit      = "safety_limit_exceeded"

	motorCommandActorTypeUser   = "user"
	motorCommandActorTypeHub    = "hub"
	motorCommandActorTypeSystem = "system"
	motorCommandActorIDSystem   = "system"
)

var activeMotorCommandStatuses = []string{
	dbMotorStatusQueued,
	dbMotorStatusLeased,
	dbMotorStatusSent,
	dbMotorStatusExecuting,
}

var _ controlv1connect.ControlServiceHandler = (*ControlService)(nil)

type ControlService struct {
	db *gorm.DB
}

func NewControlService(db *gorm.DB) *ControlService {
	return &ControlService{db: db}
}

func (s *ControlService) CreateMotorCommand(
	ctx context.Context,
	req *connect.Request[controlv1.CreateMotorCommandRequest],
) (*connect.Response[controlv1.CreateMotorCommandResponse], error) {
	userID, userIDStr, err := requireAuthenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	msg := req.Msg
	if strings.TrimSpace(msg.IdempotencyKey) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("idempotency_key is required"))
	}
	if strings.TrimSpace(msg.HubId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("hub_id is required"))
	}
	if strings.TrimSpace(msg.NodeId) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("node_id is required"))
	}

	hubID, err := parseUintID(msg.HubId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid hub_id: %w", err))
	}

	dbAction, err := validateMotorAction(msg.Action, msg.DurationMs)
	if err != nil {
		return nil, err
	}

	expiresAt, err := validateMotorExpiry(msg.ExpiresAt)
	if err != nil {
		return nil, err
	}

	if err := s.ensureOwnedNodeTarget(ctx, userID, hubID, msg.NodeId); err != nil {
		return nil, err
	}

	createdAt := time.Now().UTC()
	if err := s.reconcilePendingCommandsForTarget(ctx, userID, hubID, msg.NodeId, createdAt); err != nil {
		return nil, err
	}

	var command auth.MotorCommand
	var existing auth.MotorCommand
	returnExisting := false

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockOwnedNodeTarget(tx, userID, hubID, msg.NodeId); err != nil {
			return err
		}

		var found bool
		existing, found, err = s.findIdempotentCommandWithDB(tx, userID, msg.NodeId, msg.IdempotencyKey)
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to check idempotency: %w", err))
		}
		if found {
			returnExisting = true
			return nil
		}

		if err := s.enforceMotorCommandRateLimit(tx, userID, msg.NodeId, createdAt); err != nil {
			return err
		}

		activeCommand, found, err := s.findActiveCommandForTargetWithDB(tx, hubID, msg.NodeId)
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to check active command invariant: %w", err))
		}
		if found {
			logMotorCommandState(
				"create blocked by active command",
				activeCommand.CommandID,
				hubID,
				msg.NodeId,
				activeCommand.Status,
				activeCommand.ReasonCode,
				activeCommand.ReasonMessage,
			)
			return connect.NewError(
				connect.CodeFailedPrecondition,
				fmt.Errorf("an active command already exists for node %s (command_id=%s)", msg.NodeId, activeCommand.CommandID),
			)
		}

		commandID, err := newMotorCommandID()
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate command id: %w", err))
		}

		command = auth.MotorCommand{
			CommandID:      commandID,
			UserID:         userID,
			HubID:          hubID,
			NodeID:         msg.NodeId,
			Action:         dbAction,
			DurationMS:     int64(msg.DurationMs),
			Status:         dbMotorStatusQueued,
			IdempotencyKey: msg.IdempotencyKey,
			ReasonCode:     dbMotorReasonNone,
			ExpiresAt:      expiresAt,
		}

		if err := tx.Create(&command).Error; err != nil {
			return err
		}

		return appendMotorCommandEvent(tx, &command, motorCommandActorTypeUser, userIDStr, "", "command queued", createdAt)
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		existing, found, lookupErr := s.findIdempotentCommand(ctx, userID, msg.NodeId, msg.IdempotencyKey)
		if lookupErr == nil && found {
			return connect.NewResponse(&controlv1.CreateMotorCommandResponse{Command: motorCommandToProto(&existing)}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create command: %w", err))
	}
	if returnExisting {
		logMotorCommandState(
			"create replay returned existing command",
			existing.CommandID,
			existing.HubID,
			existing.NodeID,
			existing.Status,
			existing.ReasonCode,
			existing.ReasonMessage,
		)
		return connect.NewResponse(&controlv1.CreateMotorCommandResponse{Command: motorCommandToProto(&existing)}), nil
	}

	return connect.NewResponse(&controlv1.CreateMotorCommandResponse{Command: motorCommandToProto(&command)}), nil
}

func (s *ControlService) GetMotorCommandStatus(
	ctx context.Context,
	req *connect.Request[controlv1.GetMotorCommandStatusRequest],
) (*connect.Response[controlv1.GetMotorCommandStatusResponse], error) {
	userID, _, err := requireAuthenticatedUser(ctx)
	if err != nil {
		return nil, err
	}

	commandID := strings.TrimSpace(req.Msg.CommandId)
	if commandID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("command_id is required"))
	}

	command, err := s.getVisibleCommandByID(ctx, userID, commandID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("command not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load command status: %w", err))
	}

	command, err = s.expireVisibleCommandOnAccess(ctx, userID, command)
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to reconcile command status: %w", err))
	}

	return connect.NewResponse(&controlv1.GetMotorCommandStatusResponse{Command: motorCommandToProto(&command)}), nil
}

func (s *ControlService) PullPendingMotorCommands(
	ctx context.Context,
	req *connect.Request[controlv1.PullPendingMotorCommandsRequest],
) (*connect.Response[controlv1.PullPendingMotorCommandsResponse], error) {
	hubID, hubIDStr, err := requireHubServiceAccount(ctx)
	if err != nil {
		return nil, err
	}

	leaseDuration, err := validateMotorLeaseDuration(req.Msg.LeaseDurationMs)
	if err != nil {
		return nil, err
	}
	maxCommands, err := validatePullMaxCommands(req.Msg.MaxCommands)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var leasedCommands []auth.MotorCommand

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.cleanupExpiredAndStaleLeases(tx, hubID, hubIDStr, now); err != nil {
			return err
		}

		var queuedCommands []auth.MotorCommand
		if err := tx.
			Where("hub_id = ? AND status = ?", hubID, dbMotorStatusQueued).
			Order("created_at asc").
			Find(&queuedCommands).Error; err != nil {
			return err
		}

		leaseExpiresAt := now.Add(leaseDuration)
		for i := range queuedCommands {
			if len(leasedCommands) >= int(maxCommands) {
				break
			}

			if !queuedCommands[i].ExpiresAt.After(now) {
				continue
			}

			leaseToken, err := newMotorCommandID()
			if err != nil {
				return err
			}

			command := &queuedCommands[i]
			previousStatus := command.Status
			command.Status = dbMotorStatusLeased
			command.LeaseToken = &leaseToken
			command.LeasedAt = &now
			command.LeaseExpiresAt = &leaseExpiresAt
			command.ReasonCode = dbMotorReasonNone
			command.ReasonMessage = ""

			if err := saveMotorCommandWithEvent(tx, command, motorCommandActorTypeHub, hubIDStr, previousStatus, "command leased to hub", now); err != nil {
				return err
			}

			leasedCommands = append(leasedCommands, *command)
		}
		return nil
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to lease pending commands: %w", err))
	}

	protoCommands := make([]*controlv1.MotorCommand, 0, len(leasedCommands))
	for i := range leasedCommands {
		protoCommands = append(protoCommands, motorCommandToProto(&leasedCommands[i]))
	}

	return connect.NewResponse(&controlv1.PullPendingMotorCommandsResponse{Commands: protoCommands}), nil
}

func (s *ControlService) AckMotorCommandEvent(
	ctx context.Context,
	req *connect.Request[controlv1.AckMotorCommandEventRequest],
) (*connect.Response[controlv1.AckMotorCommandEventResponse], error) {
	hubID, hubIDStr, err := requireHubServiceAccount(ctx)
	if err != nil {
		return nil, err
	}

	commandID := strings.TrimSpace(req.Msg.CommandId)
	if commandID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("command_id is required"))
	}

	ackStatus, err := validateHubAckStatus(req.Msg.Status)
	if err != nil {
		return nil, err
	}
	ackReason := motorReasonFromProto(req.Msg.ReasonCode)
	if ackReason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported reason_code"))
	}
	ackMessage := strings.TrimSpace(req.Msg.ReasonMessage)
	now := time.Now().UTC()

	var updated auth.MotorCommand
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.cleanupExpiredAndStaleLeases(tx, hubID, hubIDStr, now); err != nil {
			return err
		}

		var command auth.MotorCommand
		if err := tx.Where("command_id = ?", commandID).First(&command).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return connect.NewError(connect.CodeNotFound, errors.New("command not found"))
			}
			return err
		}

		if command.HubID != hubID {
			return connect.NewError(connect.CodePermissionDenied, errors.New("command does not belong to authenticated hub"))
		}
		if nodeID := strings.TrimSpace(req.Msg.NodeId); nodeID != "" && nodeID != command.NodeID {
			return connect.NewError(connect.CodePermissionDenied, errors.New("command does not belong to requested node"))
		}
		if command.Status == ackStatus {
			logMotorCommandState(
				"ack ignored because status already applied",
				command.CommandID,
				command.HubID,
				command.NodeID,
				command.Status,
				command.ReasonCode,
				command.ReasonMessage,
			)
			updated = command
			return nil
		}
		if command.ExpiresAt.Before(now) || command.ExpiresAt.Equal(now) {
			logMotorCommandState(
				"ack rejected because command already expired",
				command.CommandID,
				command.HubID,
				command.NodeID,
				command.Status,
				dbMotorReasonExpired,
				command.ReasonMessage,
			)
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("command has already expired"))
		}

		if err := validateAckTransition(command.Status, ackStatus); err != nil {
			return err
		}

		previousStatus := command.Status
		command.Status = ackStatus
		command.ReasonCode = ackReason
		command.ReasonMessage = ackMessage

		if isTerminalMotorStatus(ackStatus) {
			command.CompletedAt = &now
			command.LeaseToken = nil
			command.LeasedAt = nil
			command.LeaseExpiresAt = nil
		} else if command.LeaseExpiresAt == nil || !command.LeaseExpiresAt.After(now) {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("command does not have an active lease"))
		}

		if err := saveMotorCommandWithEvent(tx, &command, motorCommandActorTypeHub, hubIDStr, previousStatus, buildAckEventMessage(ackStatus), now); err != nil {
			return err
		}

		updated = command
		return nil
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to ack command event: %w", err))
	}

	return connect.NewResponse(&controlv1.AckMotorCommandEventResponse{Command: motorCommandToProto(&updated)}), nil
}

func requireAuthenticatedUser(ctx context.Context) (uint, string, error) {
	if authctx.IsServiceAccount(ctx) {
		return 0, "", connect.NewError(connect.CodePermissionDenied, errors.New("only user tokens may manage motor commands"))
	}

	userIDStr := authctx.GetUserID(ctx)
	if userIDStr == "" || authctx.GetUsername(ctx) == "" {
		return 0, "", connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := parseUintID(userIDStr)
	if err != nil {
		return 0, "", connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid user id in token: %w", err))
	}

	return userID, userIDStr, nil
}

func requireHubServiceAccount(ctx context.Context) (uint, string, error) {
	if !authctx.IsServiceAccount(ctx) {
		return 0, "", connect.NewError(connect.CodePermissionDenied, errors.New("only hub tokens may poll or ack motor commands"))
	}

	hubIDStr := authctx.GetHubID(ctx)
	if hubIDStr == "" {
		return 0, "", connect.NewError(connect.CodePermissionDenied, errors.New("hub token is missing hub binding (re-claim via v2)"))
	}

	hubID, err := parseUintID(hubIDStr)
	if err != nil {
		return 0, "", connect.NewError(connect.CodePermissionDenied, fmt.Errorf("invalid hub id in token: %w", err))
	}

	return hubID, hubIDStr, nil
}

func parseUintID(value string) (uint, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(parsed), nil
}

func validateMotorAction(action controlv1.MotorCommandAction, durationMS int32) (string, error) {
	switch action {
	case controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION:
		if durationMS <= 0 {
			return "", connect.NewError(connect.CodeInvalidArgument, errors.New("duration_ms must be greater than zero for run_for_duration"))
		}
		if durationMS > maxMotorCommandDurationMS {
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("duration_ms must be less than or equal to %d", maxMotorCommandDurationMS))
		}
		return dbMotorActionRunForDuration, nil
	case controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_STOP:
		if durationMS != 0 {
			return "", connect.NewError(connect.CodeInvalidArgument, errors.New("duration_ms must be zero for stop"))
		}
		return dbMotorActionStop, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported action"))
	}
}

func validateMotorExpiry(expiresAtMillis int64) (time.Time, error) {
	now := time.Now().UTC()
	if expiresAtMillis == 0 {
		return now.Add(defaultMotorCommandExpiry), nil
	}

	expiresAt := time.UnixMilli(expiresAtMillis).UTC()
	if !expiresAt.After(now) {
		return time.Time{}, connect.NewError(connect.CodeInvalidArgument, errors.New("expires_at must be in the future"))
	}
	ttl := expiresAt.Sub(now)
	if ttl < minMotorCommandTTL {
		return time.Time{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("expires_at must be at least %dms in the future", minMotorCommandTTL.Milliseconds()))
	}
	if ttl > maxMotorCommandTTL {
		return time.Time{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("expires_at must be less than or equal to %dms in the future", maxMotorCommandTTL.Milliseconds()))
	}
	return expiresAt, nil
}

func validateMotorLeaseDuration(leaseDurationMS int32) (time.Duration, error) {
	if leaseDurationMS == 0 {
		return defaultMotorLeaseDuration, nil
	}
	if leaseDurationMS < 0 {
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("lease_duration_ms must be greater than zero"))
	}
	return time.Duration(leaseDurationMS) * time.Millisecond, nil
}

func validatePullMaxCommands(maxCommands int32) (int32, error) {
	if maxCommands == 0 {
		return 1, nil
	}
	if maxCommands < 0 {
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("max_commands must be greater than zero"))
	}
	if maxCommands > maxPullMotorCommands {
		return maxPullMotorCommands, nil
	}
	return maxCommands, nil
}

func validateHubAckStatus(status controlv1.MotorCommandStatus) (string, error) {
	switch status {
	case controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE:
		return dbMotorStatusSent, nil
	case controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXECUTING:
		return dbMotorStatusExecuting, nil
	case controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED:
		return dbMotorStatusSucceeded, nil
	case controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED:
		return dbMotorStatusFailed, nil
	case controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXPIRED:
		return dbMotorStatusExpired, nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported ack status"))
	}
}

func validateAckTransition(currentStatus string, nextStatus string) error {
	switch currentStatus {
	case dbMotorStatusLeased:
		if nextStatus == dbMotorStatusSent || nextStatus == dbMotorStatusExpired || nextStatus == dbMotorStatusFailed {
			return nil
		}
	case dbMotorStatusSent:
		if nextStatus == dbMotorStatusExecuting || nextStatus == dbMotorStatusSucceeded || nextStatus == dbMotorStatusFailed || nextStatus == dbMotorStatusExpired {
			return nil
		}
	case dbMotorStatusExecuting:
		if nextStatus == dbMotorStatusSucceeded || nextStatus == dbMotorStatusFailed || nextStatus == dbMotorStatusExpired {
			return nil
		}
	}

	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("invalid status transition: %s -> %s", currentStatus, nextStatus))
}

func motorReasonFromProto(reason controlv1.MotorCommandReasonCode) string {
	switch reason {
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UNSPECIFIED,
		controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE:
		return dbMotorReasonNone
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UNAUTHORIZED:
		return dbMotorReasonUnauthorized
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_EXPIRED:
		return dbMotorReasonExpired
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_DUPLICATE:
		return dbMotorReasonDuplicate
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_PROBE_UNREACHABLE:
		return dbMotorReasonProbeUnreachable
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_BLE_WRITE_FAILED:
		return dbMotorReasonBLEWriteFailed
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UART_TIMEOUT:
		return dbMotorReasonUARTTimeout
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UART_REJECTED:
		return dbMotorReasonUARTRejected
	case controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_SAFETY_LIMIT_EXCEEDED:
		return dbMotorReasonSafetyLimit
	default:
		return ""
	}
}

func isTerminalMotorStatus(status string) bool {
	switch status {
	case dbMotorStatusSucceeded, dbMotorStatusFailed, dbMotorStatusExpired, dbMotorStatusCancelled:
		return true
	default:
		return false
	}
}

func buildAckEventMessage(status string) string {
	switch status {
	case dbMotorStatusSent:
		return "command sent to probe"
	case dbMotorStatusExecuting:
		return "command executing"
	case dbMotorStatusSucceeded:
		return "command completed successfully"
	case dbMotorStatusFailed:
		return "command failed"
	case dbMotorStatusExpired:
		return "command expired"
	default:
		return "command updated"
	}
}

func appendMotorCommandEvent(
	tx *gorm.DB,
	command *auth.MotorCommand,
	actorType string,
	actorID string,
	previousStatus string,
	message string,
	occurredAt time.Time,
) error {
	event := auth.MotorCommandEvent{
		MotorCommandID: command.ID,
		CommandID:      command.CommandID,
		ActorType:      actorType,
		ActorID:        actorID,
		PreviousStatus: previousStatus,
		NewStatus:      command.Status,
		ReasonCode:     command.ReasonCode,
		ReasonMessage:  command.ReasonMessage,
		Message:        message,
		OccurredAt:     occurredAt,
	}

	if err := tx.Create(&event).Error; err != nil {
		return err
	}

	logMotorCommandTransition(&event, command)
	return nil
}

func logMotorCommandState(event string, commandID string, hubID uint, nodeID string, status string, reasonCode string, reasonMessage string) {
	log.Printf(
		"motor command %s: command_id=%s hub_id=%d node_id=%s status=%s reason_code=%s reason_message=%q",
		event,
		commandID,
		hubID,
		nodeID,
		status,
		normalizeMotorReasonCode(reasonCode),
		reasonMessage,
	)
}

func logMotorCommandTransition(event *auth.MotorCommandEvent, command *auth.MotorCommand) {
	if event == nil || command == nil {
		return
	}

	log.Printf(
		"motor command lifecycle: command_id=%s hub_id=%d node_id=%s previous_status=%s new_status=%s reason_code=%s actor_type=%s actor_id=%s message=%q",
		event.CommandID,
		command.HubID,
		command.NodeID,
		emptyMotorStatus(event.PreviousStatus),
		event.NewStatus,
		normalizeMotorReasonCode(event.ReasonCode),
		event.ActorType,
		event.ActorID,
		event.Message,
	)
}

func normalizeMotorReasonCode(reasonCode string) string {
	trimmed := strings.TrimSpace(reasonCode)
	if trimmed == "" {
		return dbMotorReasonNone
	}
	return trimmed
}

func emptyMotorStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return "none"
	}
	return status
}

func saveMotorCommandWithEvent(
	tx *gorm.DB,
	command *auth.MotorCommand,
	actorType string,
	actorID string,
	previousStatus string,
	message string,
	occurredAt time.Time,
) error {
	if err := tx.Save(command).Error; err != nil {
		return err
	}

	return appendMotorCommandEvent(tx, command, actorType, actorID, previousStatus, message, occurredAt)
}

func expireMotorCommand(
	tx *gorm.DB,
	command *auth.MotorCommand,
	actorType string,
	actorID string,
	message string,
	now time.Time,
) error {
	previousStatus := command.Status
	command.Status = dbMotorStatusExpired
	command.ReasonCode = dbMotorReasonExpired
	command.ReasonMessage = message
	command.LeaseToken = nil
	command.LeasedAt = nil
	command.LeaseExpiresAt = nil
	command.CompletedAt = &now

	return saveMotorCommandWithEvent(tx, command, actorType, actorID, previousStatus, message, now)
}

func shouldExpireMotorCommandOnAccess(command *auth.MotorCommand, now time.Time) bool {
	if command == nil {
		return false
	}

	if command.Status != dbMotorStatusQueued && command.Status != dbMotorStatusLeased {
		return false
	}

	return !command.ExpiresAt.After(now)
}

func (s *ControlService) expireVisibleCommandOnAccess(ctx context.Context, userID uint, command auth.MotorCommand) (auth.MotorCommand, error) {
	now := time.Now().UTC()
	if !shouldExpireMotorCommandOnAccess(&command, now) {
		return command, nil
	}

	var updated auth.MotorCommand
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var visible auth.MotorCommand
		if err := tx.
			Model(&auth.MotorCommand{}).
			Joins("JOIN hubs ON hubs.id = motor_commands.hub_id").
			Where("motor_commands.id = ? AND hubs.user_id = ?", command.ID, userID).
			First(&visible).Error; err != nil {
			return err
		}

		if !shouldExpireMotorCommandOnAccess(&visible, now) {
			updated = visible
			return nil
		}

		if err := expireMotorCommand(tx, &visible, motorCommandActorTypeSystem, motorCommandActorIDSystem, "command expired before delivery", now); err != nil {
			return err
		}

		updated = visible
		return nil
	})
	if err != nil {
		return auth.MotorCommand{}, err
	}

	return updated, nil
}

func (s *ControlService) cleanupExpiredAndStaleLeases(tx *gorm.DB, hubID uint, hubIDStr string, now time.Time) error {
	var commands []auth.MotorCommand
	if err := tx.
		Where("hub_id = ? AND status IN ?", hubID, []string{dbMotorStatusQueued, dbMotorStatusLeased}).
		Order("created_at asc").
		Find(&commands).Error; err != nil {
		return err
	}

	return reconcilePendingMotorCommands(tx, commands, now, motorCommandActorTypeHub, hubIDStr)
}

func (s *ControlService) cleanupPendingCommandsForTarget(tx *gorm.DB, hubID uint, nodeID string, now time.Time, actorType string, actorID string) error {
	var commands []auth.MotorCommand
	if err := tx.
		Where("hub_id = ? AND node_id = ? AND status IN ?", hubID, nodeID, []string{dbMotorStatusQueued, dbMotorStatusLeased}).
		Order("created_at asc").
		Find(&commands).Error; err != nil {
		return err
	}

	return reconcilePendingMotorCommands(tx, commands, now, actorType, actorID)
}

func (s *ControlService) reconcilePendingCommandsForTarget(ctx context.Context, userID uint, hubID uint, nodeID string, now time.Time) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockOwnedNodeTarget(tx, userID, hubID, nodeID); err != nil {
			return err
		}

		return s.cleanupPendingCommandsForTarget(tx, hubID, nodeID, now, motorCommandActorTypeSystem, motorCommandActorIDSystem)
	})
}

func reconcilePendingMotorCommands(tx *gorm.DB, commands []auth.MotorCommand, now time.Time, actorType string, actorID string) error {

	for i := range commands {
		command := &commands[i]
		previousStatus := command.Status
		leaseExpired := previousStatus == dbMotorStatusLeased && command.LeaseExpiresAt != nil && !command.LeaseExpiresAt.After(now)
		commandExpired := !command.ExpiresAt.After(now)

		if !leaseExpired && !commandExpired {
			continue
		}

		if commandExpired {
			if err := expireMotorCommand(tx, command, actorType, actorID, "command expired before delivery", now); err != nil {
				return err
			}
			continue
		}

		if leaseExpired {
			command.Status = dbMotorStatusQueued
			command.ReasonCode = dbMotorReasonNone
			command.ReasonMessage = ""
			command.LeaseToken = nil
			command.LeasedAt = nil
			command.LeaseExpiresAt = nil

			if err := saveMotorCommandWithEvent(tx, command, actorType, actorID, previousStatus, "lease expired; command requeued", now); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *ControlService) ensureOwnedNodeTarget(ctx context.Context, userID uint, hubID uint, nodeID string) error {
	return s.ensureOwnedNodeTargetWithDB(s.db.WithContext(ctx), userID, hubID, nodeID, false)
}

func (s *ControlService) lockOwnedNodeTarget(tx *gorm.DB, userID uint, hubID uint, nodeID string) error {
	return s.ensureOwnedNodeTargetWithDB(tx, userID, hubID, nodeID, true)
}

func (s *ControlService) ensureOwnedNodeTargetWithDB(db *gorm.DB, userID uint, hubID uint, nodeID string, lock bool) error {
	query := db.Model(&auth.SensorNode{}).
		Joins("JOIN hubs ON hubs.id = sensor_nodes.hub_id").
		Where("sensor_nodes.node_id = ? AND hubs.id = ? AND hubs.user_id = ?", nodeID, hubID, userID)

	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var count int64
	err := query.Count(&count).Error
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to verify node ownership: %w", err))
	}
	if count == 0 {
		return connect.NewError(connect.CodePermissionDenied, errors.New("target node is not owned by the authenticated user"))
	}
	return nil
}

func (s *ControlService) findIdempotentCommand(ctx context.Context, userID uint, nodeID string, idempotencyKey string) (auth.MotorCommand, bool, error) {
	return s.findIdempotentCommandWithDB(s.db.WithContext(ctx), userID, nodeID, idempotencyKey)
}

func (s *ControlService) findIdempotentCommandWithDB(db *gorm.DB, userID uint, nodeID string, idempotencyKey string) (auth.MotorCommand, bool, error) {
	var command auth.MotorCommand
	err := db.
		Where("user_id = ? AND node_id = ? AND idempotency_key = ?", userID, nodeID, idempotencyKey).
		First(&command).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return auth.MotorCommand{}, false, nil
	}
	if err != nil {
		return auth.MotorCommand{}, false, err
	}
	return command, true, nil
}

func (s *ControlService) findActiveCommandForTarget(ctx context.Context, hubID uint, nodeID string) (auth.MotorCommand, bool, error) {
	return s.findActiveCommandForTargetWithDB(s.db.WithContext(ctx), hubID, nodeID)
}

func (s *ControlService) findActiveCommandForTargetWithDB(db *gorm.DB, hubID uint, nodeID string) (auth.MotorCommand, bool, error) {
	var command auth.MotorCommand
	err := db.
		Where("hub_id = ? AND node_id = ? AND status IN ?", hubID, nodeID, activeMotorCommandStatuses).
		Order("created_at asc").
		First(&command).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return auth.MotorCommand{}, false, nil
	}
	if err != nil {
		return auth.MotorCommand{}, false, err
	}
	return command, true, nil
}

func (s *ControlService) enforceMotorCommandRateLimit(tx *gorm.DB, userID uint, nodeID string, now time.Time) error {
	windowStart := now.Add(-motorCommandRateLimitWindow)
	var recentCommandCount int64
	if err := tx.Model(&auth.MotorCommand{}).
		Where("user_id = ? AND node_id = ? AND created_at >= ?", userID, nodeID, windowStart).
		Count(&recentCommandCount).Error; err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to apply motor command rate limit: %w", err))
	}
	if recentCommandCount >= motorCommandRateLimitMax {
		return connect.NewError(
			connect.CodeResourceExhausted,
			fmt.Errorf("rate limit exceeded for node %s: at most %d commands per %dms", nodeID, motorCommandRateLimitMax, motorCommandRateLimitWindow.Milliseconds()),
		)
	}

	return nil
}

func (s *ControlService) getVisibleCommandByID(ctx context.Context, userID uint, commandID string) (auth.MotorCommand, error) {
	var command auth.MotorCommand
	err := s.db.WithContext(ctx).
		Model(&auth.MotorCommand{}).
		Joins("JOIN hubs ON hubs.id = motor_commands.hub_id").
		Where("motor_commands.command_id = ? AND hubs.user_id = ?", commandID, userID).
		First(&command).Error
	return command, err
}

func motorCommandToProto(command *auth.MotorCommand) *controlv1.MotorCommand {
	if command == nil {
		return nil
	}

	leaseExpiresAt := int64(0)
	if command.LeaseExpiresAt != nil {
		leaseExpiresAt = command.LeaseExpiresAt.UnixMilli()
	}

	return &controlv1.MotorCommand{
		CommandId:         command.CommandID,
		IdempotencyKey:    command.IdempotencyKey,
		HubId:             fmt.Sprint(command.HubID),
		NodeId:            command.NodeID,
		Action:            motorActionToProto(command.Action),
		DurationMs:        int32(command.DurationMS),
		RequestedByUserId: fmt.Sprint(command.UserID),
		CreatedAt:         command.CreatedAt.UnixMilli(),
		ExpiresAt:         command.ExpiresAt.UnixMilli(),
		LeaseExpiresAt:    leaseExpiresAt,
		Status:            motorStatusToProto(command.Status),
		ReasonCode:        motorReasonToProto(command.ReasonCode),
		ReasonMessage:     command.ReasonMessage,
	}
}

func motorActionToProto(action string) controlv1.MotorCommandAction {
	switch action {
	case dbMotorActionRunForDuration:
		return controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_RUN_FOR_DURATION
	case dbMotorActionStop:
		return controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_STOP
	default:
		return controlv1.MotorCommandAction_MOTOR_COMMAND_ACTION_UNSPECIFIED
	}
}

func motorStatusToProto(status string) controlv1.MotorCommandStatus {
	switch status {
	case dbMotorStatusQueued:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_QUEUED
	case dbMotorStatusLeased:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_LEASED_TO_HUB
	case dbMotorStatusSent:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SENT_TO_PROBE
	case dbMotorStatusExecuting:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXECUTING
	case dbMotorStatusSucceeded:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_SUCCEEDED
	case dbMotorStatusFailed:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_FAILED
	case dbMotorStatusExpired:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_EXPIRED
	case dbMotorStatusCancelled:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_CANCELLED
	default:
		return controlv1.MotorCommandStatus_MOTOR_COMMAND_STATUS_UNSPECIFIED
	}
}

func motorReasonToProto(reason string) controlv1.MotorCommandReasonCode {
	switch reason {
	case "", dbMotorReasonNone:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_NONE
	case dbMotorReasonUnauthorized:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UNAUTHORIZED
	case dbMotorReasonExpired:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_EXPIRED
	case dbMotorReasonDuplicate:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_DUPLICATE
	case dbMotorReasonProbeUnreachable:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_PROBE_UNREACHABLE
	case dbMotorReasonBLEWriteFailed:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_BLE_WRITE_FAILED
	case dbMotorReasonUARTTimeout:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UART_TIMEOUT
	case dbMotorReasonUARTRejected:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UART_REJECTED
	case dbMotorReasonSafetyLimit:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_SAFETY_LIMIT_EXCEEDED
	default:
		return controlv1.MotorCommandReasonCode_MOTOR_COMMAND_REASON_CODE_UNSPECIFIED
	}
}

func newMotorCommandID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16],
	), nil
}
