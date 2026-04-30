package auth

import (
	"strings"
	"sync"
	"time"
)

type RuntimeQuotaHint struct {
	Limited   bool
	RecoverAt time.Time
	Reason    string
	UpdatedAt time.Time
}

var runtimeQuotaHintByAuth sync.Map

func SetRuntimeQuotaHint(authID string, hint RuntimeQuotaHint) bool {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return false
	}
	if hint.UpdatedAt.IsZero() {
		hint.UpdatedAt = time.Now()
	}
	hint.Reason = strings.TrimSpace(hint.Reason)
	if hint.Reason == "" {
		hint.Reason = "runtime_quota"
	}
	previous, ok := GetRuntimeQuotaHint(authID)
	if ok && previous.Limited == hint.Limited && previous.Reason == hint.Reason && previous.RecoverAt.Equal(hint.RecoverAt) {
		return false
	}
	runtimeQuotaHintByAuth.Store(authID, hint)
	return true
}

func ClearRuntimeQuotaHint(authID, reason string) bool {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return false
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "runtime_quota"
	}
	hint, ok := GetRuntimeQuotaHint(authID)
	if !ok || (hint.Reason != "" && hint.Reason != reason) {
		return false
	}
	runtimeQuotaHintByAuth.Delete(authID)
	return true
}

func GetRuntimeQuotaHint(authID string) (RuntimeQuotaHint, bool) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return RuntimeQuotaHint{}, false
	}
	value, ok := runtimeQuotaHintByAuth.Load(authID)
	if !ok {
		return RuntimeQuotaHint{}, false
	}
	hint, ok := value.(RuntimeQuotaHint)
	if !ok {
		runtimeQuotaHintByAuth.Delete(authID)
		return RuntimeQuotaHint{}, false
	}
	return hint, true
}

func runtimeQuotaBlock(authID string, now time.Time) (bool, blockReason, time.Time) {
	hint, ok := GetRuntimeQuotaHint(authID)
	if !ok || !hint.Limited {
		return false, blockReasonNone, time.Time{}
	}
	if hint.RecoverAt.IsZero() || !hint.RecoverAt.After(now) {
		runtimeQuotaHintByAuth.Delete(strings.TrimSpace(authID))
		return false, blockReasonNone, time.Time{}
	}
	return true, blockReasonCooldown, hint.RecoverAt
}
