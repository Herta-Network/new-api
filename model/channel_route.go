package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// RouteCandidate is an enabled channel that serves one group and model, with
// the priority tier and the weight request routing (service.ChannelRouter)
// reads. Both come from the channel, so the memory cache and the database
// give the same candidates the same chances.
type RouteCandidate struct {
	Channel  *Channel
	Priority int64
	Weight   int
}

// ListRouteCandidates returns the enabled channels that serve modelName in
// group and pass filters, highest priority first. When no channel serves the
// exact name, it looks again with the routing name (for example without @
// modifiers). It reads the memory cache when that is on and the database
// otherwise.
func ListRouteCandidates(group, modelName string, filters []dto.ChannelFilter) ([]RouteCandidate, error) {
	if !common.MemoryCacheEnabled {
		return listRouteCandidatesFromDB(group, modelName, filters)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	ids, _ := filterCandidateIDs(group2model2channels[group][modelName], modelName, filters)
	if len(ids) == 0 {
		ids, _ = filterCandidateIDs(group2model2channels[group][ratio_setting.RoutingMatchModelName(modelName)], modelName, filters)
	}
	candidates := make([]RouteCandidate, 0, len(ids))
	for _, id := range ids {
		channel, ok := channelsIDM[id]
		if !ok {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", id)
		}
		candidates = append(candidates, RouteCandidate{Channel: channel, Priority: channel.GetPriority(), Weight: channel.GetWeight()})
	}
	return candidates, nil
}

func listRouteCandidatesFromDB(group, modelName string, filters []dto.ChannelFilter) ([]RouteCandidate, error) {
	candidates, err := routeCandidatesFromDB(group, modelName, modelName, filters)
	if err != nil {
		return nil, err
	}
	if normalized := ratio_setting.RoutingMatchModelName(modelName); len(candidates) == 0 && normalized != "" && normalized != modelName {
		return routeCandidatesFromDB(group, normalized, modelName, filters)
	}
	return candidates, nil
}

// routeCandidatesFromDB loads the enabled channels with an ability for
// abilityModel in group once, and keeps those that pass filters for
// modelName, highest priority first.
func routeCandidatesFromDB(group, abilityModel, modelName string, filters []dto.ChannelFilter) ([]RouteCandidate, error) {
	var ids []int
	if err := DB.Model(&Ability{}).Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, abilityModel, true).Distinct("channel_id").Pluck("channel_id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var channels []*Channel
	if err := DB.Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return nil, err
	}
	candidates := make([]RouteCandidate, 0, len(channels))
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue
		}
		if ok, _ := ChannelSatisfiesFilters(channel, modelName, filters); !ok {
			continue
		}
		candidates = append(candidates, RouteCandidate{Channel: channel, Priority: channel.GetPriority(), Weight: channel.GetWeight()})
	}
	slices.SortStableFunc(candidates, func(a, b RouteCandidate) int {
		switch {
		case a.Priority > b.Priority:
			return -1
		case a.Priority < b.Priority:
			return 1
		}
		return a.Channel.Id - b.Channel.Id
	})
	return candidates, nil
}

// ChannelKeyFingerprint names one key of a multi-key channel without
// revealing it, so a cooldown can be stored for that key alone. It is
// stable across processes and does not depend on the key's position.
func ChannelKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}
