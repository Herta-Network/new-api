package common

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-redis/redis/v8"
)

// RedisKeyPrefix is REDIS_KEY_PREFIX. When it is set, every key and Pub/Sub
// channel new-api uses in Redis starts with it, so an ACL user on a shared
// instance can be limited to ~<prefix>* and &<prefix>*. It is prepended as is,
// without a separator. Empty keeps the historical names byte for byte.
var RedisKeyPrefix string

// UseRedisKeyPrefix makes client send every key and channel under prefix (see
// redisName). The hook rewrites the arguments of each command, including
// MULTI/EXEC pipelines and the KEYS of EVAL, puts the prefix in the MATCH
// pattern of SCAN and removes it from the keys SCAN returns. A command missing
// from redisKeyRanges fails instead of reaching Redis unprefixed. The
// arguments are rewritten in place, so a command must not be processed twice
// (ScanCmd.Iterator, or Do with a reused argument slice).
//
// Subscriptions bypass go-redis hooks: subscribers pass channel names through
// RedisChannel, while PUBLISH is prefixed by the hook.
func UseRedisKeyPrefix(client *redis.Client, prefix string) error {
	if strings.ContainsAny(prefix, "{}") {
		return errors.New("REDIS_KEY_PREFIX must not contain { or }")
	}
	RedisKeyPrefix = prefix
	if prefix == "" {
		return nil
	}
	// Escape the glob characters of the prefix for SCAN patterns.
	var matchPrefix strings.Builder
	for i := range len(prefix) {
		if strings.IndexByte(`\*?[]`, prefix[i]) >= 0 {
			matchPrefix.WriteByte('\\')
		}
		matchPrefix.WriteByte(prefix[i])
	}
	client.AddHook(redisKeyPrefixHook{prefix: prefix, matchPrefix: matchPrefix.String()})
	return nil
}

// RedisChannel returns the Redis name of a Pub/Sub channel, for SUBSCRIBE,
// which go-redis hooks do not see.
func RedisChannel(name string) string {
	return redisName(RedisKeyPrefix, name)
}

// redisName is the name a key or channel has under prefix: the prefix, then
// the name with the prefix also at the start of its Redis Cluster hash tag.
// Keys that share a slot without the prefix, such as user:1 and
// auth:user:fence:{user:1}, then still share one with it: user:1 becomes
// <prefix>user:1 and the fence <prefix>auth:user:fence:{<prefix>user:1}.
func redisName(prefix, name string) string {
	if open := strings.IndexByte(name, '{'); open >= 0 {
		if end := strings.IndexByte(name[open+1:], '}'); end > 0 {
			return prefix + name[:open+1] + prefix + name[open+1:]
		}
	}
	return prefix + name
}

// redisKeyRange gives the key positions of a command in the form of Redis
// COMMAND INFO: the first key, the last key (negative counts from the end),
// and the step between keys. The zero value means the command has no keys.
type redisKeyRange struct {
	first, last, step int
}

// redisKeyRanges lists the commands new-api may send with a key prefix. EVAL
// and its variants, which give the number of keys, and SCAN are handled in
// prefixArgs. Commands whose keys are not at fixed positions (SORT,
// ZUNIONSTORE, XREAD, GEORADIUS ... STORE), whose replies name keys (BLPOP,
// RANDOMKEY, KEYS) or that act on the whole database (FLUSHDB) are left out on
// purpose, so they fail. Add a command here, with the positions COMMAND INFO
// reports, before using it.
var redisKeyRanges = map[string]redisKeyRange{}

func init() {
	groups := map[redisKeyRange][]string{
		{}: {"ping", "echo", "multi", "exec", "discard", "unwatch", "script"},
		{1, 1, 1}: {
			// Keys and strings.
			"expire", "pexpire", "expireat", "pexpireat", "expiretime", "pexpiretime", "persist", "ttl", "pttl",
			"type", "dump", "restore", "get", "set", "setnx", "setex", "psetex", "getset", "getdel", "getex",
			"append", "strlen", "incr", "incrby", "incrbyfloat", "decr", "decrby", "getrange", "setrange",
			"getbit", "setbit", "bitcount", "bitpos", "bitfield", "bitfield_ro",
			// Hashes.
			"hget", "hset", "hsetnx", "hmset", "hmget", "hgetall", "hdel", "hexists", "hincrby", "hincrbyfloat",
			"hkeys", "hvals", "hlen", "hstrlen", "hscan", "hrandfield",
			// Lists.
			"lpush", "rpush", "lpushx", "rpushx", "lpop", "rpop", "llen", "lindex", "lrange", "ltrim", "lrem",
			"lset", "linsert", "lpos",
			// Sets.
			"sadd", "srem", "smembers", "sismember", "smismember", "scard", "spop", "srandmember", "sscan",
			// Sorted sets.
			"zadd", "zrem", "zscore", "zmscore", "zincrby", "zcard", "zcount", "zlexcount", "zrange",
			"zrangebyscore", "zrangebylex", "zrevrange", "zrevrangebyscore", "zrevrangebylex", "zrank",
			"zrevrank", "zremrangebyrank", "zremrangebyscore", "zremrangebylex", "zpopmin", "zpopmax",
			"zrandmember", "zscan",
			// HyperLogLog, geo and streams.
			"pfadd", "geoadd", "geopos", "geodist", "geohash", "geosearch",
			"xadd", "xlen", "xrange", "xrevrange", "xdel", "xtrim", "xack", "xpending", "xclaim", "xautoclaim",
			"xsetid",
			// The channel of PUBLISH, which ACL &<prefix>* matches.
			"publish", "spublish",
		},
		{1, -1, 1}: {"del", "unlink", "exists", "touch", "mget", "watch",
			"sinter", "sunion", "sdiff", "sinterstore", "sunionstore", "sdiffstore", "pfcount", "pfmerge"},
		{1, -1, 2}: {"mset", "msetnx"},
		{1, 2, 1}:  {"rename", "renamenx", "rpoplpush", "lmove", "smove", "copy", "zrangestore", "geosearchstore"},
		{2, -1, 1}: {"bitop"},
	}
	for keyRange, names := range groups {
		for _, name := range names {
			redisKeyRanges[name] = keyRange
		}
	}
}

type redisKeyPrefixHook struct {
	prefix string
	// matchPrefix is prefix with the glob characters of a SCAN pattern escaped.
	matchPrefix string
}

func (h redisKeyPrefixHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	return ctx, h.prefixArgs(cmd)
}

func (h redisKeyPrefixHook) AfterProcess(_ context.Context, cmd redis.Cmder) error {
	h.trimScanReply(cmd)
	return nil
}

func (h redisKeyPrefixHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		if err := h.prefixArgs(cmd); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func (h redisKeyPrefixHook) AfterProcessPipeline(_ context.Context, cmds []redis.Cmder) error {
	for _, cmd := range cmds {
		h.trimScanReply(cmd)
	}
	return nil
}

func (h redisKeyPrefixHook) prefixArgs(cmd redis.Cmder) error {
	args := cmd.Args()
	name := cmd.Name()
	switch name {
	case "eval", "evalsha", "eval_ro", "evalsha_ro", "fcall", "fcall_ro":
		if len(args) < 3 {
			return fmt.Errorf("redis key prefix: %s without the number of keys", name)
		}
		numKeys, err := strconv.Atoi(fmt.Sprint(args[2]))
		if err != nil || numKeys < 0 || 3+numKeys > len(args) {
			return fmt.Errorf("redis key prefix: %s has an invalid number of keys %v", name, args[2])
		}
		return h.prefixRange(args, redisKeyRange{3, 2 + numKeys, 1})
	case "scan":
		// Without MATCH, SCAN returns other applications' keys too, which
		// trimScanReply drops.
		for i := 2; i+1 < len(args); i += 2 {
			if option, ok := args[i].(string); ok && strings.EqualFold(option, "match") {
				return prefixRedisArg(args, i+1, h.matchPrefix)
			}
		}
		return nil
	}
	keyRange, ok := redisKeyRanges[name]
	if !ok {
		return fmt.Errorf("redis key prefix: command %q is not in redisKeyRanges (common/redis_key_prefix.go)", name)
	}
	return h.prefixRange(args, keyRange)
}

func (h redisKeyPrefixHook) prefixRange(args []any, keyRange redisKeyRange) error {
	if keyRange.first == 0 {
		return nil
	}
	last := keyRange.last
	if last < 0 {
		last += len(args)
	}
	for i := keyRange.first; i <= last && i < len(args); i += keyRange.step {
		if err := prefixRedisArg(args, i, h.prefix); err != nil {
			return err
		}
	}
	return nil
}

func prefixRedisArg(args []any, i int, prefix string) error {
	switch value := args[i].(type) {
	case string:
		args[i] = redisName(prefix, value)
	case []byte:
		args[i] = redisName(prefix, string(value))
	default:
		return fmt.Errorf("redis key prefix: argument %d of %v is a %T, not a string", i, args[0], value)
	}
	return nil
}

// trimScanReply gives SCAN callers the names they use, without the prefix,
// and drops keys that are not new-api's.
func (h redisKeyPrefixHook) trimScanReply(cmd redis.Cmder) {
	scan, ok := cmd.(*redis.ScanCmd)
	if !ok || cmd.Name() != "scan" || scan.Err() != nil {
		return
	}
	keys, cursor := scan.Val()
	own := keys[:0]
	for _, key := range keys {
		name, ok := strings.CutPrefix(key, h.prefix)
		if !ok {
			continue
		}
		if open := strings.IndexByte(name, '{'); open >= 0 {
			if tag, ok := strings.CutPrefix(name[open+1:], h.prefix); ok {
				name = name[:open+1] + tag
			}
		}
		if redisName(h.prefix, name) == key {
			own = append(own, name)
		}
	}
	scan.SetVal(own, cursor)
}
