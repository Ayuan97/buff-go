package util

import "strconv"

// 字符串转float64
func StringToFloat64(str string) float64 {
	f, _ := strconv.ParseFloat(str, 64)
	return f
}

// int转float64
func IntToFloat64(i int) float64 {
	return float64(i)
}

// 字符串转int
func StringToInt(str string) int {
	i, _ := strconv.Atoi(str)
	return i
}

// 字符串转int64
func StringToInt64(str string) int64 {
	i, _ := strconv.ParseInt(str, 10, 64)
	return i
}

// int转字符串
func IntToString(i int) string {
	return strconv.Itoa(i)
}
