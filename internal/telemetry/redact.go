package telemetry

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const referenceBytes = 16

var processReferenceKey = newProcessReferenceKey()

func newProcessReferenceKey() [32]byte {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		panic("telemetry: initialize safe references: " + err.Error())
	}
	return key
}

// SafeNodeRef returns a process-scoped reference for a node ID or proxy value.
func SafeNodeRef(value string) string {
	return safeReference("node", value)
}

// SafeAccountRef returns a process-scoped reference for an account ID or session.
func SafeAccountRef(value string) string {
	return safeReference("account", value)
}

// SafeLeaseRef returns a process-scoped reference for a lease identifier.
func SafeLeaseRef(value string) string {
	return safeReference("lease", value)
}

// SafeWorkerRef returns a process-scoped reference for a worker identifier.
func SafeWorkerRef(value string) string {
	return safeReference("worker", value)
}

// SafeJobRef returns a process-scoped reference for a configured task key.
func SafeJobRef(value string) string {
	return safeReference("job", value)
}

// SafeDetailRef returns a process-scoped reference for arbitrary detail text.
func SafeDetailRef(value string) string {
	return safeReference("detail", value)
}

// SafeErrorRef returns an opaque reference for an error without exposing its text.
func SafeErrorRef(err error) string {
	if err == nil {
		return ""
	}
	return SafeDetailRef(err.Error())
}

// WrapError keeps errors.Is/errors.As behavior while exposing only a safe
// reference through Error and log formatting. Operation must be a fixed label.
func WrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "operation"
	}
	return &referencedError{operation: operation, reference: SafeErrorRef(err), cause: err}
}

type referencedError struct {
	operation string
	reference string
	cause     error
}

func (e *referencedError) Error() string {
	if e == nil {
		return "operation failed"
	}
	return fmt.Sprintf("%s failed error_ref=%s", e.operation, e.reference)
}

func (e *referencedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Format prevents debug-oriented fmt verbs from reflecting the wrapped cause.
func (e *referencedError) Format(state fmt.State, verb rune) {
	message := e.Error()
	if verb == 'q' {
		message = strconv.Quote(message)
	}
	_, _ = io.WriteString(state, message)
}

var _ interface{ Unwrap() error } = (*referencedError)(nil)
var _ fmt.Formatter = (*referencedError)(nil)

func safeReference(scope, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if validSafeReference(scope, value) {
		return value
	}
	mac := hmac.New(sha256.New, processReferenceKey[:])
	_, _ = mac.Write([]byte("value"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(scope))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	digest := hex.EncodeToString(mac.Sum(nil)[:referenceBytes])
	return scope + "_v1_" + digest + "_" + referenceTag(scope, digest)
}

func sanitizeEvent(e Event) Event {
	e.WorkerID = SafeWorkerRef(e.WorkerID)
	e.Proxy = SafeNodeRef(e.Proxy)
	e.Account = SafeAccountRef(e.Account)
	e.JobKey = SafeJobRef(e.JobKey)
	e.LeaseID = SafeLeaseRef(e.LeaseID)
	e.Detail = SafeDetailRef(e.Detail)
	return e
}

func validSafeReference(scope, value string) bool {
	prefix := scope + "_v1_"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, prefix), "_")
	if len(parts) != 2 || len(parts[0]) != referenceBytes*2 || len(parts[1]) != referenceBytes*2 {
		return false
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return false
	}
	want, err := hex.DecodeString(referenceTag(scope, parts[0]))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}

func referenceTag(scope, digest string) string {
	mac := hmac.New(sha256.New, processReferenceKey[:])
	_, _ = mac.Write([]byte("reference"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(scope))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(digest))
	return hex.EncodeToString(mac.Sum(nil)[:referenceBytes])
}
