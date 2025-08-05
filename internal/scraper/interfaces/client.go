package interfaces

import (
	"net/http"
)

// IHTTPClientManager HTTP客户端管理器接口
type IHTTPClientManager interface {
	// GetClient 获取HTTP客户端
	GetClient(config *ClientConfig) (*http.Client, error)

	// DoRequest 执行HTTP请求
	DoRequest(req *http.Request, options ...RequestOption) (*http.Response, error)

	// ReleaseClient 释放客户端资源
	ReleaseClient(client *http.Client)

	// GetStats 获取客户端统计信息
	GetStats() *ClientStats
}
