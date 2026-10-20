package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/convo"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
)

const (
	conversationSessionNamespace    = "new-api:conversation_session:v1"
	conversationSessionRedisTimeout = 2 * time.Second
)

// conversationSessionRecord is what the store keeps for one session clue: the
// session it belongs to and when the call that stored it ran.
type conversationSessionRecord struct {
	SessionID string
	At        time.Time
}

// conversationSessionStore answers convo.Lookup for session clues. Clues are
// keyed by an HMAC of principal, kind and value, so neither tier holds the text
// a client sent. Memory is always consulted first and bounded by capacity;
// Redis, when enabled, shares clues between instances. Every lookup is scoped
// to the principal, explicit clues included: tenants of one gateway may reuse
// the same prompt_cache_key or conversation id.
type conversationSessionStore struct {
	memory    *hot.HotCache[string, conversationSessionRecord]
	redis     func() *redis.Client
	keySecret []byte
	ttl       func() time.Duration
	now       func() time.Time
	// runAsync runs the Redis write off the request path.
	runAsync func(func())
}

type conversationSessionCandidate struct {
	key    string
	value  string
	rank   int
	record conversationSessionRecord
	found  bool
}

func (store *conversationSessionStore) key(principal string, kind convo.Kind, direction convo.Direction, value string) string {
	mac := hmac.New(sha256.New, store.keySecret)
	mac.Write([]byte(principal))
	mac.Write([]byte{0})
	mac.Write([]byte(kind))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return conversationSessionNamespace + ":" + string(direction) + ":" + hex.EncodeToString(mac.Sum(nil))[:32]
}

// lookup implements convo.Lookup for principal. A clue the response produced
// (DirectionOut) outranks one a request only named (DirectionIn, explicit
// clues only); within a direction the newest record wins.
func (store *conversationSessionStore) lookup(ctx context.Context, principal string, kind convo.Kind, values []string, scope convo.Scope) (convo.Match, bool, error) {
	directions := []convo.Direction{convo.DirectionOut}
	if kind == convo.KindExplicit {
		directions = append(directions, convo.DirectionIn)
	}
	candidates := make([]conversationSessionCandidate, 0, len(directions)*len(values))
	for rank, direction := range directions {
		for _, value := range values {
			candidates = append(candidates, conversationSessionCandidate{
				key:   store.key(principal, kind, direction, value),
				value: value,
				rank:  rank,
			})
		}
	}
	for index := range candidates {
		candidates[index].record, candidates[index].found, _ = store.memory.Get(candidates[index].key)
	}
	if match, ok := store.bestMatch(kind, candidates, scope); ok {
		return match, true, nil
	}

	client := store.redis()
	if client == nil || len(candidates) == 0 {
		return convo.Match{}, false, nil
	}
	keys := make([]string, len(candidates))
	for index := range candidates {
		keys[index] = candidates[index].key
	}
	replies, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return convo.Match{}, false, err
	}
	now := store.now()
	for index, reply := range replies {
		encoded, ok := reply.(string)
		if !ok || index >= len(candidates) {
			continue
		}
		atText, sessionID, ok := strings.Cut(encoded, "|")
		atMillis, err := strconv.ParseInt(atText, 10, 64)
		if !ok || err != nil || sessionID == "" {
			continue
		}
		record := conversationSessionRecord{SessionID: sessionID, At: time.UnixMilli(atMillis)}
		candidates[index].record, candidates[index].found = record, true
		if remaining := store.ttl() - now.Sub(record.At); remaining > 0 {
			store.memory.SetWithTTL(candidates[index].key, record, remaining)
		}
	}
	match, ok := store.bestMatch(kind, candidates, scope)
	return match, ok, nil
}

// bestMatch picks the winning record among the candidates that are still inside
// the window. Expiry is judged with the store's clock, not only the cache TTL,
// so it stays exact across tiers.
func (store *conversationSessionStore) bestMatch(kind convo.Kind, candidates []conversationSessionCandidate, scope convo.Scope) (convo.Match, bool) {
	now := store.now()
	ttl := store.ttl()
	var best *conversationSessionCandidate
	for index := range candidates {
		candidate := &candidates[index]
		if !candidate.found || now.Sub(candidate.record.At) > ttl {
			continue
		}
		if !scope.NotBefore.IsZero() && candidate.record.At.Before(scope.NotBefore) {
			continue
		}
		if best == nil || candidate.rank < best.rank || (candidate.rank == best.rank && candidate.record.At.After(best.record.At)) {
			best = candidate
		}
	}
	if best == nil {
		return convo.Match{}, false
	}
	return convo.Match{SessionID: best.record.SessionID, Kind: kind, Value: best.value}, true
}

// put stores the clues of one finished call under its session. Memory is
// written before returning so the next call on this instance links; the Redis
// write runs through runAsync.
func (store *conversationSessionStore) put(principal string, sessionID string, cursors []convo.Cursor) {
	if sessionID == "" || len(cursors) == 0 {
		return
	}
	record := conversationSessionRecord{SessionID: sessionID, At: store.now()}
	ttl := store.ttl()
	keys := make([]string, 0, len(cursors))
	for _, cursor := range cursors {
		key := store.key(principal, cursor.Kind, cursor.Direction, cursor.Value)
		store.memory.SetWithTTL(key, record, ttl)
		keys = append(keys, key)
	}
	client := store.redis()
	if client == nil {
		return
	}
	encoded := strconv.FormatInt(record.At.UnixMilli(), 10) + "|" + sessionID
	store.runAsync(func() {
		ctx, cancel := context.WithTimeout(context.Background(), conversationSessionRedisTimeout)
		defer cancel()
		pipe := client.Pipeline()
		for _, key := range keys {
			pipe.Set(ctx, key, encoded, ttl)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			common.SysError(common.LogText("conversation session store write failed: err=%v", err))
		}
	})
}
