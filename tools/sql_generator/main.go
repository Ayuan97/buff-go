package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"buff-go/internal/model"
)

// SQLGenerator SQL生成器
type SQLGenerator struct {
	TablePrefix string
}

// NewSQLGenerator 创建SQL生成器
func NewSQLGenerator(tablePrefix string) *SQLGenerator {
	return &SQLGenerator{
		TablePrefix: tablePrefix,
	}
}

// GoTypeToSQL Go类型到SQL类型的映射
func (g *SQLGenerator) GoTypeToSQL(goType string, fieldName string) string {
	switch goType {
	case "string":
		// 根据字段名推断长度
		if strings.Contains(strings.ToLower(fieldName), "url") ||
			strings.Contains(strings.ToLower(fieldName), "token") ||
			strings.Contains(strings.ToLower(fieldName), "session") {
			return "VARCHAR(500)"
		}
		if strings.Contains(strings.ToLower(fieldName), "password") ||
			strings.Contains(strings.ToLower(fieldName), "hash") {
			return "VARCHAR(255)"
		}
		if strings.Contains(strings.ToLower(fieldName), "name") ||
			strings.Contains(strings.ToLower(fieldName), "account") ||
			strings.Contains(strings.ToLower(fieldName), "nickname") {
			return "VARCHAR(100)"
		}
		return "VARCHAR(255)"
	case "int", "int32":
		return "INT"
	case "int64":
		if strings.Contains(strings.ToLower(fieldName), "id") {
			return "BIGINT"
		}
		if strings.Contains(strings.ToLower(fieldName), "time") ||
			strings.Contains(strings.ToLower(fieldName), "at") ||
			strings.Contains(strings.ToLower(fieldName), "update") {
			return "BIGINT"
		}
		return "BIGINT"
	case "float64":
		return "DECIMAL(10,2)"
	case "bool":
		return "TINYINT(1)"
	case "time.Time":
		return "TIMESTAMP"
	default:
		return "VARCHAR(255)"
	}
}

// ParseGormTag 解析GORM标签
func (g *SQLGenerator) ParseGormTag(tag string) map[string]string {
	result := make(map[string]string)
	if tag == "" {
		return result
	}

	parts := strings.Split(tag, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if strings.Contains(part, ":") {
			kv := strings.SplitN(part, ":", 2)
			result[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		} else {
			result[part] = "true"
		}
	}
	return result
}

// GenerateFieldSQL 生成字段SQL
func (g *SQLGenerator) GenerateFieldSQL(field reflect.StructField, fieldType reflect.Type) string {
	fieldName := g.ToSnakeCase(field.Name)
	goTypeName := fieldType.String()

	// 跳过嵌入的Model字段
	if field.Anonymous && field.Type.String() == "*model.Model" {
		return ""
	}

	sqlType := g.GoTypeToSQL(goTypeName, fieldName)

	// 解析GORM标签
	gormTag := field.Tag.Get("gorm")
	gormAttrs := g.ParseGormTag(gormTag)

	var constraints []string

	// 主键
	if _, exists := gormAttrs["primary_key"]; exists {
		constraints = append(constraints, "PRIMARY KEY")
		if fieldName == "id" {
			constraints = append(constraints, "AUTO_INCREMENT")
		}
	}

	// 非空约束
	if _, exists := gormAttrs["not_null"]; exists {
		constraints = append(constraints, "NOT NULL")
	} else if !strings.Contains(goTypeName, "*") && fieldName != "id" {
		// 非指针类型默认NOT NULL（除了ID字段）
		constraints = append(constraints, "NOT NULL")
	}

	// 唯一约束
	if _, exists := gormAttrs["unique"]; exists {
		constraints = append(constraints, "UNIQUE")
	}
	if _, exists := gormAttrs["unique_index"]; exists {
		constraints = append(constraints, "UNIQUE")
	}

	// 默认值
	defaultValue := g.GetDefaultValue(fieldName, goTypeName)
	if defaultValue != "" {
		constraints = append(constraints, fmt.Sprintf("DEFAULT %s", defaultValue))
	}

	constraintStr := ""
	if len(constraints) > 0 {
		constraintStr = " " + strings.Join(constraints, " ")
	}

	// 生成字段注释
	comment := g.GetFieldComment(field.Name, goTypeName)
	commentStr := ""
	if comment != "" {
		commentStr = fmt.Sprintf(" COMMENT '%s'", comment)
	}

	return fmt.Sprintf("    `%s` %s%s%s", fieldName, sqlType, constraintStr, commentStr)
}

// GetDefaultValue 获取字段默认值
func (g *SQLGenerator) GetDefaultValue(fieldName, goType string) string {
	fieldLower := strings.ToLower(fieldName)

	switch goType {
	case "string":
		return "''"
	case "bool":
		return "FALSE"
	case "int", "int32", "int64":
		if strings.Contains(fieldLower, "status") {
			return "0"
		}
		if strings.Contains(fieldLower, "type") {
			return "1"
		}
		if strings.Contains(fieldLower, "num") || strings.Contains(fieldLower, "count") {
			return "0"
		}
		return "0"
	case "float64":
		return "0.00"
	case "time.Time":
		if strings.Contains(fieldLower, "created") || strings.Contains(fieldLower, "updated") {
			return "CURRENT_TIMESTAMP"
		}
	}

	return ""
}

// GetFieldComment 获取字段注释
func (g *SQLGenerator) GetFieldComment(fieldName, goType string) string {
	fieldLower := strings.ToLower(fieldName)

	// 根据字段名生成中文注释
	commentMap := map[string]string{
		"id":                   "主键ID",
		"created_at":           "创建时间",
		"updated_at":           "更新时间",
		"appid":                "应用ID",
		"buy_max_price":        "最高求购价",
		"buy_num":              "求购数量",
		"game":                 "游戏名称",
		"name":                 "商品名称",
		"market_hash_name":     "市场哈希名称",
		"short_name":           "商品简称",
		"goods_id":             "商品ID",
		"icon_url":             "图标URL",
		"steam_price":          "Steam价格",
		"steam_price_cny":      "Steam价格(人民币)",
		"quick_price":          "快速出售价",
		"sell_min_price":       "最低出售价",
		"sell_num":             "出售数量",
		"sell_reference_price": "出售参考价",
		"steam_market_url":     "Steam市场URL",
		"transacted_num":       "成交数量",
		"steam_item_name_id":   "Steam物品名称ID",
		"steam_sell_price":     "Steam出售价",
		"highest_buy_order":    "最高求购订单",
		"lowest_sell_order":    "最低出售订单",
		"proportion":           "比例",
		"steam_update":         "Steam更新时间",
		"buff_update":          "Buff更新时间",
		"buff_buy_price":       "Buff求购价",
		"buff_buy_num":         "Buff求购数量",
		"buff_sell_price":      "Buff出售价",
		"buff_sell_num":        "Buff出售数量",
		"steam_buy_price":      "Steam求购价",
		"steam_buy_num":        "Steam求购数量",
		"is_push":              "是否推送",
		"is_buy":               "是否求购",
		"buff_proportion":      "Buff比例",
		"steam_proportion":     "Steam比例",
		"buff_buy_update":      "Buff求购更新",
		"buff_sell_update":     "Buff出售更新",
		"steam_buy_update":     "Steam求购更新",
		"steam_sell_update":    "Steam出售更新",
		"buff_order_id":        "Buff订单ID",
		"buff_goods_id":        "Buff商品ID",
		"buff_crt_time":        "Buff创建时间",
		"buff_price":           "Buff价格",
		"buff_pay_methods":     "Buff支付方式",
		"buff_user_id":         "Buff用户ID",
		"buff_user_nickname":   "Buff用户昵称",
		"ip":                   "IP地址",
		"type":                 "类型",
		"pass_word":            "密码",
		"proxy_type":           "代理类型",
		"country":              "国家",
		"is_https":             "是否HTTPS",
		"speed":                "速度",
		"source":               "来源",
		"port":                 "端口",
		"sessionid":            "会话ID",
		"account":              "账号",
		"password":             "密码",
		"device_id":            "设备ID",
		"csrf_token":           "CSRF令牌",
		"remember_me":          "记住我",
		"status":               "状态",
		"session_id":           "会话ID",
		"steam_country":        "Steam国家",
		"browser_id":           "浏览器ID",
		"steam_login_secure":   "Steam登录安全",
		"nickname":             "昵称",
		"username":             "用户名",
		"phone":                "手机号",
		"salt":                 "盐值",
		"avatar":               "头像",
		"balance":              "余额",
		"is_admin":             "是否管理员",
		"system_type":          "系统类型",
	}

	// 配置相关字段注释
	configCommentMap := map[string]string{
		"buff_buy_status":      "Buff求购状态",
		"buff_sell_status":     "Buff出售状态",
		"steam_buy_status":     "Steam求购状态",
		"steam_sell_status":    "Steam出售状态",
		"buff_buy_delay":       "Buff求购延迟",
		"buff_sell_delay":      "Buff出售延迟",
		"steam_buy_delay":      "Steam求购延迟",
		"steam_sell_delay":     "Steam出售延迟",
		"bot_filter":           "机器人过滤",
		"buff_page_num":        "Buff页数",
		"steam_page_num":       "Steam页数",
		"bot_buff_proportion":  "机器人Buff比例",
		"bot_steam_proportion": "机器人Steam比例",
		"bot_price":            "机器人价格",
		"min_price":            "最低价格",
		"max_price":            "最高价格",
	}

	// 其他缺失字段注释
	otherCommentMap := map[string]string{
		"market_hash_name":   "市场哈希名称",
		"short_name":         "商品简称",
		"steam_item_name_id": "Steam物品名称ID",
		"csrf_token":         "CSRF令牌",
		"remember_me":        "记住我",
		"steam_login_secure": "Steam登录安全",
		"highest_buy_order":  "最高求购订单",
		"lowest_sell_order":  "最低出售订单",
		"is_push":            "是否推送",
		"is_buy":             "是否求购",
		"buff_proportion":    "Buff比例",
		"steam_proportion":   "Steam比例",
	}

	// 合并注释映射
	for k, v := range configCommentMap {
		commentMap[k] = v
	}
	for k, v := range otherCommentMap {
		commentMap[k] = v
	}

	if comment, exists := commentMap[fieldLower]; exists {
		return comment
	}

	// 根据字段名模式生成注释
	if strings.Contains(fieldLower, "price") {
		return "价格"
	}
	if strings.Contains(fieldLower, "num") || strings.Contains(fieldLower, "count") {
		return "数量"
	}
	if strings.Contains(fieldLower, "time") || strings.Contains(fieldLower, "update") {
		return "时间"
	}
	if strings.Contains(fieldLower, "url") {
		return "链接地址"
	}
	if strings.Contains(fieldLower, "id") {
		return "ID"
	}
	if strings.Contains(fieldLower, "status") {
		return "状态"
	}

	return ""
}

// ToSnakeCase 转换为蛇形命名
func (g *SQLGenerator) ToSnakeCase(str string) string {
	var result strings.Builder
	for i, r := range str {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteRune('_')
		}
		result.WriteRune(r)
	}
	return strings.ToLower(result.String())
}

// GenerateTableSQL 生成表创建SQL
func (g *SQLGenerator) GenerateTableSQL(modelType reflect.Type, tableName string) string {
	var fields []string
	var indexes []string

	// 添加基础Model字段
	fields = append(fields, "    `id` BIGINT PRIMARY KEY AUTO_INCREMENT COMMENT '主键ID'")
	fields = append(fields, "    `created_at` BIGINT NOT NULL DEFAULT 0 COMMENT '创建时间'")
	fields = append(fields, "    `updated_at` BIGINT NOT NULL DEFAULT 0 COMMENT '更新时间'")

	// 处理结构体字段
	for i := 0; i < modelType.NumField(); i++ {
		field := modelType.Field(i)
		fieldType := field.Type

		// 跳过嵌入的Model字段
		if field.Anonymous && field.Type.String() == "*model.Model" {
			continue
		}

		fieldSQL := g.GenerateFieldSQL(field, fieldType)
		if fieldSQL != "" {
			fields = append(fields, fieldSQL)
		}

		// 生成索引
		gormTag := field.Tag.Get("gorm")
		gormAttrs := g.ParseGormTag(gormTag)
		fieldName := g.ToSnakeCase(field.Name)

		if _, exists := gormAttrs["index"]; exists {
			indexes = append(indexes, fmt.Sprintf("CREATE INDEX idx_%s_%s ON %s (`%s`);",
				tableName, fieldName, g.TablePrefix+tableName, fieldName))
		}
		if _, exists := gormAttrs["unique_index"]; exists {
			indexes = append(indexes, fmt.Sprintf("CREATE UNIQUE INDEX idx_%s_%s ON %s (`%s`);",
				tableName, fieldName, g.TablePrefix+tableName, fieldName))
		}
	}

	sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s%s` (\n%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;",
		g.TablePrefix, tableName, strings.Join(fields, ",\n"))

	if len(indexes) > 0 {
		sql += "\n\n" + strings.Join(indexes, "\n")
	}

	return sql
}

// GenerateAllTablesSQL 生成所有表的SQL
func (g *SQLGenerator) GenerateAllTablesSQL() string {
	var allSQL []string

	// 定义所有模型和对应的表名
	models := map[string]reflect.Type{
		"goods":      reflect.TypeOf(model.Goods{}),
		"info":       reflect.TypeOf(model.Info{}),
		"order":      reflect.TypeOf(model.Order{}),
		"ip":         reflect.TypeOf(model.Ip{}),
		"buff_user":  reflect.TypeOf(model.BuffUser{}),
		"steam_user": reflect.TypeOf(model.SteamUser{}),
		"user":       reflect.TypeOf(model.User{}),
		"system":     reflect.TypeOf(model.System{}),
		"config":     reflect.TypeOf(model.Config{}),
	}

	// 添加SQL文件头部注释
	header := fmt.Sprintf(`-- ========================================
-- 数据库表创建SQL文件
-- 生成时间: %s
-- 项目: buff-go
-- 说明: 根据Go模型自动生成的数据库表结构
-- ========================================

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

`, time.Now().Format("2006-01-02 15:04:05"))

	allSQL = append(allSQL, header)

	// 为每个模型生成表SQL
	for tableName, modelType := range models {
		tableSQL := g.GenerateTableSQL(modelType, tableName)
		allSQL = append(allSQL, fmt.Sprintf("-- %s 表\n%s\n", tableName, tableSQL))
	}

	// 添加额外的索引和约束
	additionalSQL := g.GenerateAdditionalConstraints()
	if additionalSQL != "" {
		allSQL = append(allSQL, additionalSQL)
	}

	// 添加文件尾部
	footer := `
SET FOREIGN_KEY_CHECKS = 1;

-- ========================================
-- SQL文件生成完成
-- ========================================`

	allSQL = append(allSQL, footer)

	return strings.Join(allSQL, "\n")
}

// GenerateAdditionalConstraints 生成额外的约束和索引
func (g *SQLGenerator) GenerateAdditionalConstraints() string {
	var constraints []string

	constraints = append(constraints, "-- 额外的索引和约束")

	// 为常用查询字段添加索引
	indexes := []string{
		fmt.Sprintf("CREATE INDEX idx_goods_appid ON %sgoods (`appid`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_goods_game ON %sgoods (`game`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_goods_market_hash_name ON %sgoods (`market_hash_name`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_info_appid ON %sinfo (`appid`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_info_market_hash_name ON %sinfo (`market_hash_name`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_order_buff_goods_id ON %sorder (`buff_goods_id`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_order_buff_user_id ON %sorder (`buff_user_id`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_ip_type ON %sip (`type`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_ip_country ON %sip (`country`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_buff_user_status ON %sbuff_user (`status`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_steam_user_status ON %ssteam_user (`status`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_user_username ON %suser (`username`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_user_phone ON %suser (`phone`);", g.TablePrefix),
		fmt.Sprintf("CREATE INDEX idx_user_status ON %suser (`status`);", g.TablePrefix),
	}

	constraints = append(constraints, indexes...)

	return strings.Join(constraints, "\n")
}

func main() {
	// 创建SQL生成器，使用空前缀（根据项目配置可能有表前缀）
	generator := NewSQLGenerator("")

	// 生成所有表的SQL
	sql := generator.GenerateAllTablesSQL()

	// 创建输出目录
	outputDir := "storage/sql"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("创建输出目录失败: %v\n", err)
		return
	}

	// 写入SQL文件
	filename := fmt.Sprintf("%s/create_tables_%s.sql", outputDir, time.Now().Format("20060102_150405"))
	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("创建SQL文件失败: %v\n", err)
		return
	}
	defer file.Close()

	_, err = file.WriteString(sql)
	if err != nil {
		fmt.Printf("写入SQL文件失败: %v\n", err)
		return
	}

	fmt.Printf("SQL文件生成成功: %s\n", filename)
	fmt.Printf("包含以下数据表:\n")
	fmt.Println("- goods (商品信息)")
	fmt.Println("- info (商品详细信息)")
	fmt.Println("- order (订单信息)")
	fmt.Println("- ip (代理IP信息)")
	fmt.Println("- buff_user (Buff用户信息)")
	fmt.Println("- steam_user (Steam用户信息)")
	fmt.Println("- user (系统用户信息)")
	fmt.Println("- system (系统配置)")
	fmt.Println("- config (系统配置详情)")
}
