package main

import (
	"math"
)

const (
	RADIUS = 6371000 // 地球平均半径（米）
)

// 纬度、经度的位置。
type location struct {
	lat, long float64
}

// 用余弦法计算球面距离
func distance(p1, p2 location) float64 {
	s1, c1 := math.Sincos(rad(p1.lat))
	s2, c2 := math.Sincos(rad(p2.lat))
	clong := math.Cos(rad(p1.long - p2.long))
	return RADIUS * math.Acos(s1*s2+c1*c2*clong)
}

// 度数转换成弧度
func rad(deg float64) float64 {
	return deg * math.Pi / 180
}
