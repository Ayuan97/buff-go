package framework

import (
	"buff-go/global"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrorHandler 错误处理器实现
type ErrorHandler struct {
	maxRetryCount int
	baseDelay     time.Duration
}

// NewErrorHandler 创建错误处理器
func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{
		maxRetryCount: 3,
		baseDelay:     time.Second,
	}
}

// HandleError 处理错误
func (eh *ErrorHandler) HandleError(err error, context map[string]interface{}) *ScrapingError {
	if err == nil {
		return nil
	}

	scrapingErr := &ScrapingError{
		Message:   err.Error(),
		Timestamp: time.Now(),
		Retryable: eh.IsRetryable(err),
	}

	// 根据错误类型分类
	scrapingErr.Type = eh.classifyError(err)
	scrapingErr.Code = eh.getErrorCode(scrapingErr.Type)

	// 添加上下文信息
	if context != nil {
		if url, ok := context["url"].(string); ok {
			scrapingErr.Details = fmt.Sprintf("URL: %s", url)
		}
		if proxy, ok := context["proxy"].(string); ok {
			scrapingErr.Details += fmt.Sprintf(", Proxy: %s", proxy)
		}
	}

	// 记录错误
	eh.LogError(scrapingErr)

	return scrapingErr
}

// IsRetryable 判断错误是否可重试
func (eh *ErrorHandler) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errorType := eh.classifyError(err)

	switch errorType {
	case ErrorTypeNetwork, ErrorTypeTimeout, ErrorTypeProxy:
		return true
	case ErrorTypeRateLimit:
		return true
	case ErrorTypeAuth:
		return false // 认证错误通常不可重试
	case ErrorTypeParsing, ErrorTypeValidation:
		return false // 解析和验证错误通常不可重试
	case ErrorTypeSystem:
		return false // 系统错误通常不可重试
	default:
		return false
	}
}

// GetRetryDelay 获取重试延迟
func (eh *ErrorHandler) GetRetryDelay(retryCount int) time.Duration {
	if retryCount <= 0 {
		return eh.baseDelay
	}

	// 指数退避策略
	delay := eh.baseDelay
	for i := 0; i < retryCount && i < 5; i++ {
		delay *= 2
	}

	// 最大延迟不超过30秒
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}

	return delay
}

// LogError 记录错误
func (eh *ErrorHandler) LogError(err *ScrapingError) {
	if global.Logger != nil {
		global.Logger.WithFields(map[string]interface{}{
			"type":      err.Type,
			"code":      err.Code,
			"retryable": err.Retryable,
			"details":   err.Details,
		}).Error(err.Message)
	} else {
		fmt.Printf("[ERROR] %s - %s (Type: %d, Code: %s, Retryable: %t)\n",
			err.Timestamp.Format("2006-01-02 15:04:05"),
			err.Message,
			err.Type,
			err.Code,
			err.Retryable)
	}
}

// classifyError 分类错误
func (eh *ErrorHandler) classifyError(err error) ErrorType {
	if err == nil {
		return ErrorTypeSystem
	}

	errStr := strings.ToLower(err.Error())

	// 网络相关错误
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "no route to host") {
		return ErrorTypeNetwork
	}

	// 超时错误
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return ErrorTypeTimeout
	}

	// 代理相关错误
	if strings.Contains(errStr, "proxy") ||
		strings.Contains(errStr, "socks") {
		return ErrorTypeProxy
	}

	// 认证错误
	if strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "forbidden") ||
		strings.Contains(errStr, "authentication") {
		return ErrorTypeAuth
	}

	// 限流错误
	if strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests") {
		return ErrorTypeRateLimit
	}

	// 解析错误
	if strings.Contains(errStr, "json") ||
		strings.Contains(errStr, "xml") ||
		strings.Contains(errStr, "parse") ||
		strings.Contains(errStr, "unmarshal") {
		return ErrorTypeParsing
	}

	// 验证错误
	if strings.Contains(errStr, "validation") ||
		strings.Contains(errStr, "invalid") {
		return ErrorTypeValidation
	}

	// 检查具体的错误类型
	switch err.(type) {
	case *net.DNSError:
		return ErrorTypeNetwork
	case *net.OpError:
		return ErrorTypeNetwork
	case *url.Error:
		if urlErr, ok := err.(*url.Error); ok {
			return eh.classifyError(urlErr.Err)
		}
		return ErrorTypeNetwork
	case *http.ProtocolError:
		return ErrorTypeNetwork
	}

	// 检查context错误
	if err == context.DeadlineExceeded {
		return ErrorTypeTimeout
	}
	if err == context.Canceled {
		return ErrorTypeSystem
	}

	// 默认为系统错误
	return ErrorTypeSystem
}

// getErrorCode 获取错误代码
func (eh *ErrorHandler) getErrorCode(errorType ErrorType) string {
	switch errorType {
	case ErrorTypeNetwork:
		return "NETWORK_ERROR"
	case ErrorTypeTimeout:
		return "TIMEOUT_ERROR"
	case ErrorTypeProxy:
		return "PROXY_ERROR"
	case ErrorTypeAuth:
		return "AUTH_ERROR"
	case ErrorTypeRateLimit:
		return "RATE_LIMIT_ERROR"
	case ErrorTypeParsing:
		return "PARSING_ERROR"
	case ErrorTypeValidation:
		return "VALIDATION_ERROR"
	case ErrorTypeSystem:
		return "SYSTEM_ERROR"
	default:
		return "UNKNOWN_ERROR"
	}
}

// CreateNetworkError 创建网络错误
func (eh *ErrorHandler) CreateNetworkError(message string, details string) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeNetwork,
		Code:      "NETWORK_ERROR",
		Message:   message,
		Details:   details,
		Timestamp: time.Now(),
		Retryable: true,
	}
}

// CreateTimeoutError 创建超时错误
func (eh *ErrorHandler) CreateTimeoutError(message string, details string) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeTimeout,
		Code:      "TIMEOUT_ERROR",
		Message:   message,
		Details:   details,
		Timestamp: time.Now(),
		Retryable: true,
	}
}

// CreateProxyError 创建代理错误
func (eh *ErrorHandler) CreateProxyError(message string, details string) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeProxy,
		Code:      "PROXY_ERROR",
		Message:   message,
		Details:   details,
		Timestamp: time.Now(),
		Retryable: true,
	}
}

// CreateAuthError 创建认证错误
func (eh *ErrorHandler) CreateAuthError(message string, details string) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeAuth,
		Code:      "AUTH_ERROR",
		Message:   message,
		Details:   details,
		Timestamp: time.Now(),
		Retryable: false,
	}
}

// CreateRateLimitError 创建限流错误
func (eh *ErrorHandler) CreateRateLimitError(message string, details string) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeRateLimit,
		Code:      "RATE_LIMIT_ERROR",
		Message:   message,
		Details:   details,
		Timestamp: time.Now(),
		Retryable: true,
	}
}

// CreateParsingError 创建解析错误
func (eh *ErrorHandler) CreateParsingError(message string, details string) *ScrapingError {
	return &ScrapingError{
		Type:      ErrorTypeParsing,
		Code:      "PARSING_ERROR",
		Message:   message,
		Details:   details,
		Timestamp: time.Now(),
		Retryable: false,
	}
}

// ShouldRetry 判断是否应该重试
func (eh *ErrorHandler) ShouldRetry(err error, retryCount int) bool {
	if retryCount >= eh.maxRetryCount {
		return false
	}

	return eh.IsRetryable(err)
}
