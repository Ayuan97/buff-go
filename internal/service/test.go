package service

import "fmt"

func Test() {
	config := myDao.GetOneSystemConfig(1)
	fmt.Println(config)
}
