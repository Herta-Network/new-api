package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetGroupUsageCountsLiveUsersAndTokensWithAGroup(t *testing.T) {
	truncateTables(t)
	users := make([]*User, 0, 4)
	for index, group := range []string{"default", "default", "vip", "vip"} {
		user := &User{
			Username: fmt.Sprintf("group-usage-%d", index),
			Password: "password123",
			Role:     common.RoleCommonUser,
			Status:   common.UserStatusEnabled,
			Group:    group,
			AffCode:  fmt.Sprintf("gu%02d", index),
		}
		require.NoError(t, DB.Create(user).Error)
		users = append(users, user)
	}
	require.NoError(t, DB.Delete(users[3]).Error)
	var deletedToken *Token
	for index, group := range []string{"vip", "auto", "", "", "vip"} {
		token := &Token{
			UserId: users[0].Id,
			Key:    fmt.Sprintf("group-usage-token-%d", index),
			Name:   fmt.Sprintf("token %d", index),
			Group:  group,
		}
		require.NoError(t, DB.Create(token).Error)
		deletedToken = token
	}
	require.NoError(t, DB.Delete(deletedToken).Error)

	usage, err := GetGroupUsage()

	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"default": 2, "vip": 1}, usage.Users)
	assert.Equal(t, map[string]int64{"vip": 1, "auto": 1}, usage.Tokens)
}
