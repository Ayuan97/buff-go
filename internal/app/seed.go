package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"buff-go/internal/ratelimit"
	"buff-go/internal/storage/postgres"
)

// steamRateLimitSpecs 是 Steam 限频策略的唯一事实来源。依据见
// platforms/steam/search-render.md 的静默阶梯实测：
//
//   - 平台最小间隔 2s（0.5 req/s）是本地自限，不是已验证的 Steam QPS。同一个
//     接口在 1.84 req/s 撞过 429、在 2.25 req/s 连打 4862 次没撞，容量不稳定
//     （文档记为「287/394 不是稳定容量」），所以取一个明显低于两者的值。
//   - 接口冷却 60s。撞 429 之后同一个出口、同一份会话手动请求仍然能拿到 200，
//     说明这类 429 不是把出口整个锁住，一小时的等待是白等。文档里静默 45 分钟
//     仍 429 的长锁记录是另一种情形（桶被压测打空），单靠原因码分不出来。
//
// 代价是真撞上长锁时会每分钟白撞一次。要两头都准得按连续 429 次数退避，
// 那需要在限频状态上记住连击次数。
func steamRateLimitSpecs() []ratelimit.PolicySpec {
	return []ratelimit.PolicySpec{
		{
			Platform:        "steam",
			RuleKey:         "local_pacing",
			Scope:           ratelimit.ScopePlatform,
			Kind:            ratelimit.KindMinInterval,
			MinInterval:     2 * time.Second,
			DefaultCooldown: time.Minute,
		},
		{
			Platform:        "steam",
			RuleKey:         "search_render_429",
			Scope:           ratelimit.ScopeInterface,
			EndpointClass:   "market_summary",
			Kind:            ratelimit.KindCooldownOnly,
			DefaultCooldown: time.Minute,
		},
		{
			// orderbook 从未测出 429（登录 2000+1033、匿名 9935 次全 200），
			// 这里的 60s 是照搬 search/render，没有自己的实测依据。
			Platform:        "steam",
			RuleKey:         "orderbook_unexhausted",
			Scope:           ratelimit.ScopeInterface,
			EndpointClass:   "market_orderbook",
			Kind:            ratelimit.KindCooldownOnly,
			DefaultCooldown: time.Minute,
		},
	}
}

// seedSteamRateLimits 让库里的策略收敛到代码定义的值。只创建不更新会让改过的
// 常量对已有库无效，那样代码就不再是事实来源。
func seedSteamRateLimits(ctx context.Context, store *postgres.Store) error {
	existing, err := store.ListRateLimitPolicies(ctx)
	if err != nil {
		return fmt.Errorf("read steam rate-limit policies: %w", err)
	}
	current := make(map[ratelimit.RuleKey]ratelimit.Policy, len(existing))
	for _, policy := range existing {
		if policy.Spec.Platform == "steam" {
			current[policy.Spec.RuleKey] = policy
		}
	}
	for _, spec := range steamRateLimitSpecs() {
		policy, found := current[spec.RuleKey]
		if !found {
			if _, err := store.CreateRateLimitPolicy(ctx, spec, true); err != nil &&
				!errors.Is(err, postgres.ErrRateLimitPolicyConflict) {
				return fmt.Errorf("seed steam rate-limit %s: %w", spec.RuleKey, err)
			}
			continue
		}
		if policy.Spec == spec && policy.Enabled {
			continue
		}
		// 替换会重置节流状态并重新预热，冷却事实保留，所以正在生效的惩罚不会被绕过
		if _, err := store.ReplaceRateLimitPolicy(ctx, policy.ID, policy.Revision, spec, true); err != nil {
			return fmt.Errorf("update steam rate-limit %s: %w", spec.RuleKey, err)
		}
	}
	return nil
}
