package service

type Pool struct {
	Ip     string
	Port   int
	Https  int
	Status int
}

// 获取代理
func getProxy() {

}

// 代理失效 移出代理池 30s后再试
func FailProxy() {

}
