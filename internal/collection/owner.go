package collection

import (
	"context"
	"errors"
)

// ErrOwnerFence 表示当前常驻采集实例已经被新的 owner epoch 取代。
var ErrOwnerFence = errors.New("collection daemon owner epoch is stale")

// OwnerEpoch 是同一数据库 schema 内常驻采集实例的持久代次。
type OwnerEpoch int64

type ownerEpochContextKey struct{}

// WithOwnerEpoch 把已经取得实例锁的 owner epoch 绑定到 daemon 调用链。
func WithOwnerEpoch(ctx context.Context, epoch OwnerEpoch) context.Context {
	if ctx == nil {
		panic("nil owner epoch context")
	}
	if epoch < 1 {
		panic("owner epoch must be positive")
	}
	return context.WithValue(ctx, ownerEpochContextKey{}, epoch)
}

// OwnerEpochFromContext 返回 daemon 调用链携带的 owner epoch。
func OwnerEpochFromContext(ctx context.Context) (OwnerEpoch, bool) {
	if ctx == nil {
		return 0, false
	}
	epoch, ok := ctx.Value(ownerEpochContextKey{}).(OwnerEpoch)
	return epoch, ok && epoch > 0
}
