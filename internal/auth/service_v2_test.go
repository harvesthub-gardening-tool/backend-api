package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthService_AssociateHub(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "associate@example.com", "password123")
	require.NoError(t, err)

	t.Run("successfully associates a hub", func(t *testing.T) {
		hubID, err := service.AssociateHub(ctx, userID, "device-001", "secret-abc", "Garden Hub")
		require.NoError(t, err)
		assert.NotEmpty(t, hubID)

		var hub Hub
		require.NoError(t, service.db.Where("device_id = ?", "device-001").First(&hub).Error)
		assert.Equal(t, "Garden Hub", hub.Name)
		require.NotNil(t, hub.HubSecretHash)
		assert.NotEqual(t, "secret-abc", *hub.HubSecretHash)
		assert.Len(t, *hub.HubSecretHash, 64)
	})

	t.Run("rejects duplicate device_id", func(t *testing.T) {
		_, err := service.AssociateHub(ctx, userID, "device-dup", "secret-1", "Hub A")
		require.NoError(t, err)

		userID2, err := service.RegisterUser(ctx, "second@example.com", "password123")
		require.NoError(t, err)

		_, err = service.AssociateHub(ctx, userID2, "device-dup", "secret-2", "Hub B")
		assert.ErrorIs(t, err, ErrDeviceAlreadyAssociated)
	})

	t.Run("rejects empty inputs", func(t *testing.T) {
		_, err := service.AssociateHub(ctx, "", "d", "s", "n")
		assert.Error(t, err)
		_, err = service.AssociateHub(ctx, userID, "", "s", "n")
		assert.Error(t, err)
		_, err = service.AssociateHub(ctx, userID, "d", "", "n")
		assert.Error(t, err)
		_, err = service.AssociateHub(ctx, userID, "d", "s", "")
		assert.Error(t, err)
	})
}

func TestAuthService_ClaimHubToken(t *testing.T) {
	service, jwtManager := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "claim@example.com", "password123")
	require.NoError(t, err)

	t.Run("successfully claims a token", func(t *testing.T) {
		_, err := service.AssociateHub(ctx, userID, "claim-dev-1", "claim-secret", "Greenhouse")
		require.NoError(t, err)

		token, err := service.ClaimHubToken(ctx, "claim-dev-1", "claim-secret")
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		claims, err := jwtManager.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, userID, claims.UserID)
		assert.Empty(t, claims.Username)
		assert.True(t, claims.IsServiceAccount())
	})

	t.Run("rejects unknown device_id", func(t *testing.T) {
		_, err := service.ClaimHubToken(ctx, "nonexistent", "any")
		assert.ErrorIs(t, err, ErrInvalidDeviceCredentials)
	})

	t.Run("rejects wrong hub_secret", func(t *testing.T) {
		_, err := service.AssociateHub(ctx, userID, "claim-dev-2", "right-secret", "H2")
		require.NoError(t, err)

		_, err = service.ClaimHubToken(ctx, "claim-dev-2", "wrong-secret")
		assert.ErrorIs(t, err, ErrInvalidDeviceCredentials)
	})

	t.Run("enforces claim-once", func(t *testing.T) {
		_, err := service.AssociateHub(ctx, userID, "claim-dev-3", "s3", "H3")
		require.NoError(t, err)

		_, err = service.ClaimHubToken(ctx, "claim-dev-3", "s3")
		require.NoError(t, err)

		_, err = service.ClaimHubToken(ctx, "claim-dev-3", "s3")
		assert.ErrorIs(t, err, ErrHubAlreadyClaimed)
	})
}

func TestAuthService_ListHubs(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "listhubs@example.com", "password123")
	require.NoError(t, err)

	t.Run("returns empty list", func(t *testing.T) {
		hubs, err := service.ListHubs(ctx, userID)
		require.NoError(t, err)
		assert.Empty(t, hubs)
	})

	t.Run("returns associated hubs with claim status", func(t *testing.T) {
		userID2, err := service.RegisterUser(ctx, "listhubs2@example.com", "password123")
		require.NoError(t, err)

		_, err = service.AssociateHub(ctx, userID2, "lh-dev-1", "s1", "Unclaimed Hub")
		require.NoError(t, err)
		_, err = service.AssociateHub(ctx, userID2, "lh-dev-2", "s2", "Claimed Hub")
		require.NoError(t, err)
		_, err = service.ClaimHubToken(ctx, "lh-dev-2", "s2")
		require.NoError(t, err)

		hubs, err := service.ListHubs(ctx, userID2)
		require.NoError(t, err)
		assert.Len(t, hubs, 2)

		statusByDevice := map[string]HubInfo{}
		for _, h := range hubs {
			statusByDevice[h.DeviceID] = h
		}
		assert.False(t, statusByDevice["lh-dev-1"].Claimed)
		assert.True(t, statusByDevice["lh-dev-2"].Claimed)
		assert.False(t, statusByDevice["lh-dev-2"].Revoked)
	})

	t.Run("rejects empty userID", func(t *testing.T) {
		_, err := service.ListHubs(ctx, "")
		assert.Error(t, err)
	})
}

func TestAuthService_RevokeHub(t *testing.T) {
	service, _ := setupTestService(t)
	ctx := context.Background()

	userID, err := service.RegisterUser(ctx, "revokehub@example.com", "password123")
	require.NoError(t, err)

	t.Run("successfully revokes claimed hub and allows re-claim", func(t *testing.T) {
		hubID, err := service.AssociateHub(ctx, userID, "rev-dev-1", "rs1", "RH1")
		require.NoError(t, err)
		_, err = service.ClaimHubToken(ctx, "rev-dev-1", "rs1")
		require.NoError(t, err)

		require.NoError(t, service.RevokeHub(ctx, userID, hubID))

		var count int64
		require.NoError(t, service.db.Model(&HubToken{}).Where("hub_id = ?", hubID).Count(&count).Error)
		assert.Equal(t, int64(0), count, "hub_tokens row should be hard-deleted to permit re-claim")

		_, err = service.ClaimHubToken(ctx, "rev-dev-1", "rs1")
		assert.NoError(t, err, "re-claim must succeed after revoke")
	})

	t.Run("succeeds for unclaimed hub (no-op on tokens)", func(t *testing.T) {
		hubID, err := service.AssociateHub(ctx, userID, "rev-dev-2", "rs2", "RH2")
		require.NoError(t, err)
		assert.NoError(t, service.RevokeHub(ctx, userID, hubID))
	})

	t.Run("rejects hub owned by another user", func(t *testing.T) {
		other, err := service.RegisterUser(ctx, "other-rev@example.com", "password123")
		require.NoError(t, err)
		hubID, err := service.AssociateHub(ctx, other, "rev-dev-3", "rs3", "RH3")
		require.NoError(t, err)

		err = service.RevokeHub(ctx, userID, hubID)
		assert.ErrorIs(t, err, ErrHubNotFound)
	})

	t.Run("rejects nonexistent hub", func(t *testing.T) {
		err := service.RevokeHub(ctx, userID, "999999")
		assert.ErrorIs(t, err, ErrHubNotFound)
	})

	t.Run("rejects empty inputs", func(t *testing.T) {
		assert.Error(t, service.RevokeHub(ctx, "", "1"))
		assert.Error(t, service.RevokeHub(ctx, userID, ""))
	})
}
