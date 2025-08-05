package error

import (
	"buff-go/internal/scraper/interfaces"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"
)

// ErrorHandler 错误处理器实现
type ErrorHandler struct {
	maxRetryDelay time.Duration
	baseDelay     time.Duration
}

// NewErrorHandler 创建错误处理器
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{
		maxRetryDelay: 5 * time.Minute,
		baseDelay:     time.Second,
	}
}

// HandleError 处理错误
func (eh *ErrorHandler) HandleError(err error, context map[string]interface{}) *interfaces.ScrapingError {
	if err == nil {
		return nil
	}

	scrapingErr := &interfaces.ScrapingError{
		Message:   err.Error(),
		Details:   fmt.Sprintf("Context: %+v", context),
		Timestamp: time.Now(),
		Retryable: eh.IsRetryable(err),
	}

	// 根据错误类型设置错误代码和类型
	scrapingErr.Type, scrapingErr.Code = eh.classifyError(err)

	return scrapingErr
}

// IsRetryable 判断错误是否可重试
func (eh *ErrorHandler) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// 网络相关错误通常可重试
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "temporary failure") {
		return true
	}

	// 检查是否是网络错误
	if netErr, ok := err.(net.Error); ok {
		return netErr.Temporary() || netErr.Timeout()
	}

	// URL错误通常不可重试
	if _, ok := err.(*url.Error); ok {
		return eh.IsRetryable(err.(*url.Error).Err)
	}

	// HTTP状态码相关
	if strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "429") { // Too Many Requests
		return true
	}

	// 4xx错误通常不可重试（除了429）
	if strings.Contains(errStr, "400") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "404") {
		return false
	}

	return false
}

// GetRetryDelay 获取重试延迟
func (eh *ErrorHandler) GetRetryDelay(retryCount int) time.Duration {
	if retryCount <= 0 {
		return eh.baseDelay
	}

	// 指数退避算法
	delay := eh.baseDelay
	for i := 0; i < retryCount; i++ {
		delay *= 2
		if delay > eh.maxRetryDelay {
			delay = eh.maxRetryDelay
			break
		}
	}

	return delay
}

// LogError 记录错误
func (eh *ErrorHandler) LogError(err *interfaces.ScrapingError) {
	if err == nil {
		return
	}

	logMsg := fmt.Sprintf("[%s] %s - %s (Code: %s, Retryable: %v)",
		eh.errorTypeToString(err.Type),
		err.Message,
		err.Details,
		err.Code,
		err.Retryable,
	)

	log.Printf("ERROR: %s", logMsg)
}

// classifyError 分类错误
func (eh *ErrorHandler) classifyError(err error) (interfaces.ErrorType, string) {
	if err == nil {
		return interfaces.ErrorTypeSystem, "UNKNOWN"
	}

	errStr := strings.ToLower(err.Error())

	// 网络错误
	if strings.Contains(errStr, "timeout") {
		return interfaces.ErrorTypeTimeout, "TIMEOUT"
	}

	if strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "host") {
		return interfaces.ErrorTypeNetwork, "NETWORK"
	}

	// 代理错误
	if strings.Contains(errStr, "proxy") ||
		strings.Contains(errStr, "socks") {
		return interfaces.ErrorTypeProxy, "PROXY"
	}

	// 认证错误
	if strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "forbidden") ||
		strings.Contains(errStr, "403") {
		return interfaces.ErrorTypeAuth, "AUTH"
	}

	// 限流错误
	if strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests") {
		return interfaces.ErrorTypeRateLimit, "RATE_LIMIT"
	}

	// 解析错误
	if strings.Contains(errStr, "json") ||
		strings.Contains(errStr, "xml") ||
		strings.Contains(errStr, "parse") ||
		strings.Contains(errStr, "unmarshal") {
		return interfaces.ErrorTypeParsing, "PARSING"
	}

	// 验证错误
	if strings.Contains(errStr, "validation") ||
		strings.Contains(errStr, "invalid") ||
		strings.Contains(errStr, "bad request") ||
		strings.Contains(errStr, "400") {
		return interfaces.ErrorTypeValidation, "VALIDATION"
	}

	// 默认为系统错误
	return interfaces.ErrorTypeSystem, "SYSTEM"
}

// errorTypeToString 将错误类型转换为字符串
func (eh *ErrorHandler) errorTypeToString(et interfaces.ErrorType) string {
	switch et {
	case interfaces.ErrorTypeNetwork:
		return "NETWORK"
	case interfaces.ErrorTypeTimeout:
		return "TIMEOUT"
	case interfaces.ErrorTypeProxy:
		return "PROXY"
	case interfaces.ErrorTypeAuth:
		return "AUTH"
	case interfaces.ErrorTypeRateLimit:
		return "RATE_LIMIT"
	case interfaces.ErrorTypeParsing:
		return "PARSING"
	case interfaces.ErrorTypeValidation:
		return "VALIDATION"
	case interfaces.ErrorTypeSystem:
		return "SYSTEM"
	default:
		return "UNKNOWN"
	}
}

// GetErrorStats 获取错误统计信息
func (eh *ErrorHandler) GetErrorStats() map[string]int {
	// 这里可以实现错误统计的逻辑
	// 目前返回空的统计信息
	return map[string]int{
		"network":    0,
		"timeout":    0,
		"proxy":      0,
		"auth":       0,
		"rate_limit": 0,
		"parsing":    0,
		"validation": 0,
		"system":     0,
	}
}

// SetMaxRetryDelay 设置最大重试延迟
func (eh *ErrorHandler) SetMaxRetryDelay(delay time.Duration) {
	eh.maxRetryDelay = delay
}

// SetBaseDelay 设置基础延迟
func (eh *ErrorHandler) SetBaseDelay(delay time.Duration) {
	eh.baseDelay = delay
}
