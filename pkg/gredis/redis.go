package gredis

import (
	"buff-go/global"
	"context"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

var ctx = context.Background()

// 删除所有key
func DelAll() {
	keys := global.Redis.Keys(ctx, "*").Val()
	for _, key := range keys {
		global.Redis.Del(ctx, key)
	}
}

// set hash
func Hset(key string, field string, value interface{}) error {
	key = global.RedisSetting.Prefix + key
	err := global.Redis.HSet(ctx, key, field, value).Err()
	if err != nil {
		global.Logger.Errorf("redis hset failed %v", err)
		return err
	}
	return nil
}

// get hash
func Hget(key string, field string) string {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.HGet(ctx, key, field).Result()
	if err != nil {
		global.Logger.Errorf("redis hget failed %v", err)
		return ""
	}
	return val
}

// get all hash
func HGetAll(key string) (map[string]string, map[string]string) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.HGetAll(ctx, key).Result()
	if err != nil {
		global.Logger.Errorf("redis hget failed %v", err)
		return nil, nil
	}
	return val, nil
}

// Set a key/value
func Set(key string, data interface{}, expiration time.Duration) error {
	key = global.RedisSetting.Prefix + key
	err := global.Redis.Set(ctx, key, data, expiration).Err()
	if err != nil {
		global.Logger.Errorf("redis set failed %v", err)
		return err
	}
	return nil
}

// Set a key
func Get(key string) string {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.Get(ctx, key).Result()
	if err != nil {
		//global.Logger.Errorf("redis get failed %v", err)
		return ""
	}
	return val
}
func Del(key string) (res int64, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.Del(ctx, key).Result()
	if err != nil {
		global.Logger.Errorf("redis Del failed %v", err)
		return 0, err
	}
	return val, nil
}

func Zadd(key string, members redis.Z) (int64, error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZAdd(ctx, key, &members).Result()
	if err != nil {
		global.Logger.Errorf("redis zadd failed %v", err)
		return 0, err
	}
	return val, nil
}
func Zscore(key string, uid int) (int64, int64) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZScore(ctx, key, strconv.Itoa(uid)).Result()
	if err != nil {
		global.Logger.Errorf("redis ZScore failed %v", err)
		return 0, 0
	}
	return int64(val), 0

}

func Zrem(key string, members ...interface{}) (int64, error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZRem(ctx, key, members).Result()
	if err != nil {
		global.Logger.Errorf("redis zrem failed %v", err)
		return 0, err
	}
	return val, nil
}

func Incr(key string) (int64, error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.Incr(ctx, key).Result()
	if err != nil {
		global.Logger.Errorf("redis incr failed %v", err)
		return 0, err
	}
	return val, nil
}

func Decr(key string) (int64, error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.Decr(ctx, key).Result()
	if err != nil {
		global.Logger.Errorf("redis decr failed %v", err)
		return 0, err
	}
	return val, nil
}

// 无序集合相关
func SAdd(key string, members ...interface{}) (res int64) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.SAdd(ctx, key, members).Result()
	if err != nil {
		global.Logger.Errorf("redis sadd failed %v", err)
		return 0
	}
	return val
}

func SRem(key string, members ...interface{}) (res int64, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.SRem(ctx, key, members).Result()
	if err != nil {
		global.Logger.Errorf("redis srem failed %v", err)
		return 0, err
	}
	return val, nil
}

func SIsMember(key string, members interface{}) (res bool) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.SIsMember(ctx, key, members).Result()
	if err != nil {
		global.Logger.Errorf("redis SIsMember failed %v", err)
		return false
	}
	return val
}

func SMembers(key string) (res []string) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.SMembers(ctx, key).Result()
	if err != nil {
		global.Logger.Errorf("redis SMembers failed %v", err)
		var res []string
		return res
	}
	return val
}

func Zcard(key string) (res int64, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZCard(ctx, key).Result()
	if err != nil {
		global.Logger.Errorf("redis ZCard failed %v", err)
		return 0, err
	}
	return val, nil
}

func ZunionStore(key string, z *redis.ZStore) (res int64, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZUnionStore(ctx, key, z).Result()
	if err != nil {
		global.Logger.Errorf("redis ZUnionStore failed %v", err)
		return 2212312, err
	}
	return val, nil
}

func Zremrangebyrank(key string, start int64, end int64) (res int64, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZRemRangeByRank(ctx, key, start, end).Result()
	if err != nil {
		global.Logger.Errorf("redis ZRemRangeByRank failed %v", err)
		return 0, err
	}
	return val, nil
}
func Zrangebyscore(key string, opt *redis.ZRangeBy) (res []string, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZRangeByScore(ctx, key, opt).Result()
	if err != nil {
		global.Logger.Errorf("redis ZRangeByScore failed %v", err)
		return nil, err
	}
	return val, nil
}
func Zrange(key string, start int64, end int64) (res []string, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZRange(ctx, key, start, end).Result()
	if err != nil {
		global.Logger.Errorf("redis ZRange failed %v", err)
		return nil, err
	}
	return val, nil

}
func Zrevrange(key string, start int64, end int64) (res []string, err error) {
	key = global.RedisSetting.Prefix + key
	val, err := global.Redis.ZRevRange(ctx, key, start, end).Result()
	if err != nil {
		global.Logger.Errorf("redis ZRevRange failed %v", err)
		return nil, err
	}
	return val, nil
}

// 队列  入队列
func LPush(key string, values ...interface{}) (res int64, err error) {
	key = global.RedisSetting.Prefix + key
	result, err := global.Redis.LPush(ctx, key, values).Result()
	if err != nil {
		//global.Logger.Errorf("redis LPush failed %v", err)
		return 0, err
	}
	return result, nil

}

// 队列  出队列
func RPop(key string) (res string, err error) {
	key = global.RedisSetting.Prefix + key
	result, err := global.Redis.RPop(ctx, key).Result()
	if err != nil {
		//global.Logger.Errorf("redis LPush failed %v", err)
		return "", err
	}
	return result, nil

}

// 返回集合中的一个随机元素
func Srandmember(key string) *redis.StringCmd {
	key = global.RedisSetting.Prefix + key
	result := global.Redis.SRandMember(ctx, key)
	return result
}

func Srem(key string, name string) int64 {
	key = global.RedisSetting.Prefix + key
	result := global.Redis.SRem(ctx, key, name)
	return result.Val()
}

// Publish 发布消息到指定频道
func Publish(channel string, message interface{}) error {
	channel = global.RedisSetting.Prefix + channel
	err := global.Redis.Publish(ctx, channel, message).Err()
	if err != nil {
		global.Logger.Errorf("redis publish failed %v", err)
		return err
	}
	return nil
}

// Subscribe 订阅指定频道
func Subscribe(channels ...string) *redis.PubSub {
	// 添加前缀
	prefixedChannels := make([]string, len(channels))
	for i, channel := range channels {
		prefixedChannels[i] = global.RedisSetting.Prefix + channel
	}
	return global.Redis.Subscribe(ctx, prefixedChannels...)
}
