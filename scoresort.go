// 根据玩家总分进行排序的实现
package main

type ClientSlice []*Client

func (s ClientSlice) Len() int           { return len(s) }
func (s ClientSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ClientSlice) Less(i, j int) bool { return s[i].totalScore > s[j].totalScore } // 降序排序
