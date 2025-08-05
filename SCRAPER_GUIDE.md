# 数据抓取系统使用指南

## 概述

本系统是一个现代化的数据抓取框架，支持多种数据源的并发抓取，具有完善的错误处理、重试机制、代理管理和数据处理功能。

## 架构特点

- **模块化设计**：框架层、业务层、实现层分离
- **高并发**：支持协程池和任务队列
- **高可用**：完善的错误处理和重试机制
- **高性能**：多级缓存和连接池优化
- **可扩展**：易于添加新的抓取器类型
- **可监控**：详细的统计信息和价格变化监控

## 支持的抓取器类型

1. **buff_buy** - Buff买入数据抓取
2. **buff_sell** - Buff卖出数据抓取
3. **steam_buy** - Steam买入数据抓取
4. **steam_sell** - Steam卖出数据抓取

## 快速开始

### 1. 环境准备

```bash
# 确保Go版本 >= 1.19
go version

# 安装依赖
go mod tidy
```

### 2. 配置数据库

```sql
-- 创建数据库
CREATE DATABASE buff_go CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

-- 导入表结构（根据项目中的SQL文件）
```

### 3. 配置Redis

```bash
# 启动Redis服务
redis-server

# 测试连接
redis-cli ping
```

### 4. 修改配置文件

复制并修改配置文件：

```bash
cp configs/scraper_config.yaml configs/config.yaml
# 编辑 configs/config.yaml，修改数据库和Redis连接信息
```

### 5. 启动抓取器

```bash
# 启动单个抓取器
go run cmd/scraper/main.go -scrapers=buff_buy

# 启动多个抓取器
go run cmd/scraper/main.go -scrapers=buff_buy,buff_sell,steam_buy,steam_sell

# 使用自定义配置文件
go run cmd/scraper/main.go -scrapers=buff_buy -config=./configs/custom.yaml
```

## 命令行参数

- `-scrapers`: 指定要启动的抓取器类型，多个用逗号分隔
- `-config`: 指定配置文件路径（可选）
- `-help`: 显示帮助信息

## 配置说明

### 抓取器配置

每个抓取器都支持以下配置项：

- `enabled`: 是否启用
- `max_concurrency`: 最大并发数
- `request_delay`: 请求延迟（毫秒）
- `retry_count`: 重试次数
- `timeout`: 请求超时时间（毫秒）
- `use_proxy`: 是否使用代理
- `enable_cache`: 是否启用缓存
- `cache_ttl`: 缓存过期时间（秒）

### 代理配置

- `enabled`: 是否启用代理
- `max_fail_count`: 最大失败次数
- `health_check_url`: 健康检查URL
- `check_interval`: 检查间隔（秒）
- `refresh_interval`: 刷新间隔（秒）

## 监控和日志

### 状态监控

系统会每30秒输出一次抓取器状态：

```
抓取器状态: map[buff_buy:running buff_sell:running]
```

### 日志文件

日志文件位于 `storage/logs/` 目录下：

- `app.log`: 应用日志
- `error.log`: 错误日志

### 性能指标

系统会记录以下性能指标：

- 请求成功率
- 平均响应时间
- 错误分布
- 代理使用情况

## 开发指南

### 添加新的抓取器类型

1. 在 `internal/scraper/framework/scraper_factory.go` 中添加新的抓取器类型
2. 实现 `createXXXScraper` 方法
3. 在 `internal/scraper/business/data_processor.go` 中添加数据处理逻辑
4. 更新配置文件模板

### 自定义数据处理

继承 `DataProcessor` 并重写相应的处理方法：

```go
func (dp *CustomDataProcessor) ProcessBuffBuyData(items []BuffBuyItem, game string, appid int) *ProcessingResult {
    // 自定义处理逻辑
    return result
}
```

## 故障排除

### 常见问题

1. **数据库连接失败**
   - 检查数据库配置
   - 确保数据库服务正在运行
   - 验证用户权限

2. **Redis连接失败**
   - 检查Redis配置
   - 确保Redis服务正在运行
   - 验证连接参数

3. **代理不可用**
   - 检查代理配置
   - 验证代理服务器状态
   - 查看代理健康检查日志

4. **抓取失败**
   - 检查目标网站是否可访问
   - 验证请求头和参数
   - 查看错误日志

### 调试模式

启用调试模式获取更详细的日志：

```yaml
Server:
  RunMode: debug
```

## 性能优化

### 并发调优

根据系统资源调整并发数：

```yaml
Scraper:
  BuffBuy:
    max_concurrency: 10  # 增加并发数
```

### 缓存优化

合理设置缓存TTL：

```yaml
Cache:
  default_ttl: 600  # 增加缓存时间
```

### 代理优化

使用高质量代理提高成功率：

```yaml
Proxy:
  max_fail_count: 3  # 降低失败阈值
```

## 安全注意事项

1. **请求频率控制**：避免过于频繁的请求
2. **代理轮换**：定期更换代理IP
3. **用户代理**：使用真实的浏览器User-Agent
4. **错误处理**：妥善处理各种异常情况

## 更新日志

### v2.0.0 (当前版本)
- 重构为统一框架架构
- 支持四种抓取器类型
- 完善的错误处理和重试机制
- 统一的配置管理
- 实时状态监控

### v1.x.x (遗留版本)
- 独立的抓取脚本
- 基础的数据抓取功能
