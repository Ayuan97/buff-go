package collection

import (
	"time"

	"buff-go/internal/market"
	"buff-go/internal/resource"
)

// BlockOrErr 是控制面与结构化日志共用的稳定拦截/失败码。
// 调度内部仍写 TargetReason / WorkerWaitReason；对外映射到这套码，不另起持久化词表。
type BlockOrErr string

const (
	BlockSessionInvalid     BlockOrErr = "session_invalid"
	BlockEgressCNBlocked    BlockOrErr = "egress_cn_blocked"
	BlockEgressUnavailable  BlockOrErr = "egress_unavailable"
	BlockResourceIncomplete BlockOrErr = "resource_incomplete"
	BlockLeaseHeld          BlockOrErr = "lease_held"
	BlockHTTP429            BlockOrErr = "http_429"
	BlockHTTP5xx            BlockOrErr = "http_5xx"
	BlockAuthFail           BlockOrErr = "auth_fail"
	BlockParseFail          BlockOrErr = "parse_fail"
	BlockTimeout            BlockOrErr = "timeout"
)

// ObserveDirection 把行情边映射成日志/控制面的 sell|buy。
func ObserveDirection(side market.Side) string {
	switch side {
	case market.SideAsk:
		return "sell"
	case market.SideBid:
		return "buy"
	default:
		return ""
	}
}

// RetryAfterSec 把绝对恢复时刻收成 UI 可用的剩余秒数。已到期返回 0。
func RetryAfterSec(until, now time.Time) int {
	if until.IsZero() || now.IsZero() || !until.After(now) {
		return 0
	}
	wait := until.Sub(now)
	sec := int((wait + time.Second - 1) / time.Second)
	if sec < 1 {
		return 1
	}
	return sec
}

// MapTargetReason 把已有目标原因映射到稳定码。空串表示不是本切片要暴露的拦截。
func MapTargetReason(reason TargetReason) BlockOrErr {
	switch reason {
	case TargetReasonSessionInvalid:
		return BlockSessionInvalid
	case TargetReasonEgressCNBlocked:
		return BlockEgressCNBlocked
	case TargetReasonEgressUnavailable:
		return BlockEgressUnavailable
	case TargetReasonNoCombination, TargetReasonResourceIncomplete,
		TargetReasonMissingRatePolicy, TargetReasonInvalidConfig, TargetReasonInterfaceUnverified:
		return BlockResourceIncomplete
	default:
		return ""
	}
}

// MapWaitReason 把工人退避原因映射到稳定采集码。网络/临时失败没有对应稳定码。
func MapWaitReason(reason WorkerWaitReason) BlockOrErr {
	switch reason {
	case WorkerWaitReasonRateLimit, WorkerWaitReasonDeferred:
		return BlockHTTP429
	case WorkerWaitReasonTimeout:
		return BlockTimeout
	default:
		return ""
	}
}

// EffectiveBlock 优先用显式码，否则从退避原因回推。
func (wait WorkerWait) EffectiveBlock() BlockOrErr {
	if wait.BlockOrErr != "" {
		return wait.BlockOrErr
	}
	return MapWaitReason(wait.Reason)
}

// TargetBlockOrErr 只在目标处于拦截/失败态时返回稳定码。
func TargetBlockOrErr(target Target) BlockOrErr {
	switch target.Actual() {
	case ActualBlocked, ActualError:
		return MapTargetReason(target.Reason())
	default:
		return ""
	}
}

func combinationBlockReason(
	current resource.CombinationResources,
	target resource.TargetRegion,
	now time.Time,
) TargetReason {
	if current.Account.SessionState != resource.AccountSessionStateValid &&
		current.Account.SessionState != resource.AccountSessionStateUnverified {
		return TargetReasonSessionInvalid
	}
	if current.Node.State == resource.NodeStateValidating || current.Node.ExitVerification == nil {
		return TargetReasonResourceIncomplete
	}
	if !current.Node.Region.Allows(target) {
		if current.Node.Region == resource.NodeRegionDomestic && target == resource.TargetRegionForeign {
			return TargetReasonEgressCNBlocked
		}
		return TargetReasonEgressUnavailable
	}
	if !current.Node.UsableAt(now) {
		return TargetReasonEgressUnavailable
	}
	return TargetReasonNone
}
