package main

import (
	"math"
	"math/rand"
	"time"
)

func randInt(min int, max int) int {
	if max-min <= 1 {
		return min
	}
	return min + rand.Intn(max-min)
}

// 时间戳
func MakeTimestamp() int64 {
	return time.Now().Unix()
}

// 四舍五入， f是原浮点数，n是保留小数点位数
func Round(f float64, n int) float64 {
	pow10N := math.Pow10(n)
	return math.Trunc(f*pow10N+0.5) / pow10N
}

// 类似Python中的in，判断列表是否存在指定的元素
func IntInSlice(a int, list []int) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}
