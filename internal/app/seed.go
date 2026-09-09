package app

import (
	"context"
	"fmt"
	"time"

	"buff-go/internal/ratelimit"
)

const steamRequestInterval = 2 * time.Second
const steamDaemonInterval = 2 * time.Second

var obsoleteSteamRateLimitRuleKeys = []ratelimit.RuleKey{
	"local_pacing",
	"search_render_429",
	"orderbook_unexhausted",
}

type rateLimitPolicyStore interface {
	ListRateLimitPolicies(context.Context) ([]ratelimit.Policy, error)
	CreateRateLimitPolicy(context.Context, ratelimit.PolicySpec, bool) (ratelimit.Policy, error)
	ReplaceRateLimitPolicy(context.Context, ratelimit.PolicyID, int64, ratelimit.PolicySpec, bool) (ratelimit.Policy, error)
}

// steamRateLimitSpecs 是 Steam 限频策略的唯一事实来源。依据见
// platforms/steam/search-render.md 的静默阶梯记录：
//
//   - 单个接口、账号与出口 IP 组合的最小间隔 2s（0.5 req/s）是本地自限，
//     不是已验证的 Steam QPS。同一个
//     接口在 1.84 req/s 撞过 429、在 2.25 req/s 连打 4862 次没撞，容量不稳定
//     （文档记为「287/394 不是稳定容量」），所以取一个明显低于两者的值。
//   - ask 与 bid 使用不同接口桶；429 只冷却命中的接口、账号与出口 IP 组合。
//   - 第一次 429 冷却 60s。再探仍 429 升到 3 分钟，第三次起 10 分钟封顶。
//     成功 HTTP 清掉连击。
func steamRateLimitSpecs() []ratelimit.PolicySpec {
	return []ratelimit.PolicySpec{
		{
			Platform:        "steam",
			RuleKey:         "summary_account_exit_pacing",
			Scope:           ratelimit.ScopeAccountIP,
			EndpointClass:   "market_summary",
			Kind:            ratelimit.KindMinInterval,
			MinInterval:     steamRequestInterval,
			DefaultCooldown: time.Minute,
		},
		{
			Platform:        "steam",
			RuleKey:         "orderbook_account_exit_pacing",
			Scope:           ratelimit.ScopeAccountIP,
			EndpointClass:   "market_orderbook",
			Kind:            ratelimit.KindMinInterval,
			MinInterval:     steamRequestInterval,
			DefaultCooldown: time.Minute,
		},
	}
}

// seedSteamRateLimits 让库里的策略收敛到代码定义的值。只创建不更新会让改过的
// 常量对已有库无效，那样代码就不再是事实来源。
func seedSteamRateLimits(ctx context.Context, store rateLimitPolicyStore) error {
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
			if _, err := store.CreateRateLimitPolicy(ctx, spec, true); err != nil {
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
	// 新规则全部启用后再停旧桶；中途失败只会暂时更保守，不会留下无限频窗口。
	for _, key := range obsoleteSteamRateLimitRuleKeys {
		policy, found := current[key]
		if !found || !policy.Enabled {
			continue
		}
		if _, err := store.ReplaceRateLimitPolicy(ctx, policy.ID, policy.Revision, policy.Spec, false); err != nil {
			return fmt.Errorf("disable obsolete steam rate-limit %s: %w", key, err)
		}
	}
	return nil
}
