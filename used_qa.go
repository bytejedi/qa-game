package main

import (
	"fmt"
	"go.uber.org/zap"

	CO "qa-game/config"
)

type UsedAdBuffer struct {
	ads map[string][]int // 玩家答过的所有的广告题，map[玩家username][]广告题ID
	add chan *Client     // 添加已经玩过的广告题到已经玩过的广告题列表
	get chan *Client     // 查询此玩家答过的所有的广告题
}

var usedAdBuf *UsedAdBuffer

func init() {
	usedAdBuf = &UsedAdBuffer{ // 信道不能加缓冲区，确保ads数据的一致性
		ads: make(map[string][]int),
		add: make(chan *Client),
		get: make(chan *Client),
	}
	InitAllUsedAd()
	go UsedAdBufferManager()
}

func InitAllUsedAd() {
	db := CO.DB()
	defer db.Close()

	rows, err := db.Query(fmt.Sprintf("SELECT username,qid FROM super_game_log WHERE escape=0"))
	if err != nil {
		Logger.Error("db.Query", zap.NamedError("ctt", err))
	}
	for rows.Next() {
		var username string
		var qid int

		err := rows.Scan(&username, &qid)
		if err != nil {
			Logger.Error("rows.Scan", zap.NamedError("ctt", err))
		}

		usedAdBuf.ads[username] = append(usedAdBuf.ads[username], qid)
	}
}

func (c *Client) addUsedAd() {
	if c.adQAs != nil {
		var qids []int
		for i := range c.adQAs {
			qids = append(qids, c.adQAs[i].Id)
		}
		usedAdBuf.ads[c.profile.UserName] = append(usedAdBuf.ads[c.profile.UserName], qids...)
	}
}

func (c *Client) getUsedAds() []int {
	if _, ok := usedAdBuf.ads[c.profile.UserName]; ok { // 玩家有玩过的广告题
		return usedAdBuf.ads[c.profile.UserName]
	}
	return nil // 新玩家，从来没有玩过广告题
}

func UsedAdBufferManager() {
	for {
		select {
		case c := <-usedAdBuf.add: // 把玩家刚刚答过的一道广告题放到已玩过的广告题列表，map[username]qid
			c.addUsedAd()
		case c := <-usedAdBuf.get: // 查询此玩家玩过的所有的广告题
			c.usedAdQAs = c.getUsedAds()
			c.getUsedAdsDone <- true
		}
	}
}
