package cache

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// Entity type constants — used as key prefixes.
const (
	EntityPost    = "post"
	EntityComment = "comment"
	EntityPostTag = "post_tag"
)

// voteScript atomically:
//   1. Reads the user's previous vote from the votes hash.
//   2. Updates (or deletes) the user's entry in the votes hash.
//   3. Tracks the user in a "deleted" set if their new vote is 0.
//   4. Increments the score key by the delta.
//   5. Marks the entity dirty so the flush worker knows to sync it.
//
// KEYS: [1]=votes:{type}:{id}  [2]=score:{type}:{id}
//       [3]=dirty:{type}       [4]=deleted:{type}:{id}
// ARGV: [1]=userID  [2]=newVote (-1|0|+1)  [3]=entityID (string, for SADD)
var voteScript = redis.NewScript(`
local votes_key   = KEYS[1]
local score_key   = KEYS[2]
local dirty_key   = KEYS[3]
local deleted_key = KEYS[4]

local user_id   = ARGV[1]
local new_vote  = tonumber(ARGV[2])
local entity_id = ARGV[3]

local old_str  = redis.call('HGET', votes_key, user_id)
local old_vote = 0
if old_str and old_str ~= false then
    old_vote = tonumber(old_str)
end

local delta = new_vote - old_vote

if new_vote == 0 then
    redis.call('HDEL', votes_key, user_id)
    -- remember to delete from DB during flush
    redis.call('SADD', deleted_key, user_id)
else
    redis.call('HSET', votes_key, user_id, tostring(new_vote))
    -- if the user is re-voting after a retract, un-mark as deleted
    redis.call('SREM', deleted_key, user_id)
end

if delta ~= 0 then
    redis.call('INCRBY', score_key, delta)
    redis.call('SADD', dirty_key, entity_id)
end

return delta
`)

// CastVote records a vote in Redis.  newVote must be -1, 0 (retract), or +1.
// Returns the net delta applied to the score (useful for client-side updates).
func CastVote(ctx context.Context, rdb *redis.Client, entityType string, entityID, userID int32, newVote int16) (int64, error) {
	idStr := strconv.FormatInt(int64(entityID), 10)
	userStr := strconv.FormatInt(int64(userID), 10)

	keys := []string{
		votesKey(entityType, idStr),
		scoreKey(entityType, idStr),
		dirtyKey(entityType),
		deletedKey(entityType, idStr),
	}
	argv := []interface{}{userStr, int(newVote), idStr}

	res, err := voteScript.Run(ctx, rdb, keys, argv...).Int64()
	if err != nil {
		return 0, fmt.Errorf("cache: cast vote: %w", err)
	}
	return res, nil
}

// GetScore returns the current net score for an entity from Redis.
// On a cache miss it returns (0, redis.Nil) — callers should fall back to the
// DB score column and seed the cache value themselves.
func GetScore(ctx context.Context, rdb *redis.Client, entityType string, entityID int32) (int64, error) {
	idStr := strconv.FormatInt(int64(entityID), 10)
	val, err := rdb.Get(ctx, scoreKey(entityType, idStr)).Int64()
	if err != nil {
		return 0, err // may be redis.Nil — caller checks
	}
	return val, nil
}

// SeedScore sets the score cache for an entity only if the key does not already
// exist (SET NX).  Used during cold-start preload to avoid overwriting live data.
func SeedScore(ctx context.Context, rdb *redis.Client, entityType string, entityID int32, score int64) error {
	idStr := strconv.FormatInt(int64(entityID), 10)
	return rdb.SetNX(ctx, scoreKey(entityType, idStr), score, 0).Err()
}

// GetUserVote returns the current user's vote for an entity from the Redis
// hash.  Returns 0 if the user has not voted or the entity is not in cache.
func GetUserVote(ctx context.Context, rdb *redis.Client, entityType string, entityID, userID int32) (int16, error) {
	idStr := strconv.FormatInt(int64(entityID), 10)
	userStr := strconv.FormatInt(int64(userID), 10)

	val, err := rdb.HGet(ctx, votesKey(entityType, idStr), userStr).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("cache: get user vote: %w", err)
	}
	return int16(val), nil
}

// ── Key helpers ───────────────────────────────────────────────────────────────

func votesKey(entityType, id string) string   { return "votes:" + entityType + ":" + id }
func scoreKey(entityType, id string) string   { return "score:" + entityType + ":" + id }
func dirtyKey(entityType string) string        { return "dirty:" + entityType }
func deletedKey(entityType, id string) string { return "deleted:" + entityType + ":" + id }
