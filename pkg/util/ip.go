package util

import "qingshanyoufeng/pkg/util/iploc"

func GetIPLoc(ip string) string {
	country, _ := iploc.Find(ip)
	return country
}
