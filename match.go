package main

import (
	"container/heap"
	"time"
)

// 配对好友对战
func (c *Client) matchInvite() {
	if c.inviterUsername != "" { // 被邀请人
		// 检查邀请人是否还在线
		if inviter, ok := c.hub.friends[c.inviterUsername]; ok { // 邀请人在线
			// 创建房间
			room := &Room{
				rank:                     c.lastGameRank,
				clients:                  make(map[*Client]bool),
				questionsAndRightAnswers: make([]QA, 0),
				gameType:                 3,
			}
			// 注册用户到房间
			c.room = room
			inviter.room = room
			room.clients[inviter] = true
			room.clients[c] = true
			// 查询数据库获取一套题和对应答案,并存入room对象
			room.getQuestionsAndRightAnswers()
			// 注册房间
			c.hub.roomRegister <- room
		} else { // 邀请人已离线
			c.send <- []byte(`{"code": 403, "data": "", "msg": "好友已离线或已经在游戏中", "type": "err"}`)
		}
	}
}

// 配对非好友对战玩家(包括普通对战，排位赛对战),创建房间
func (c *Client) match(pq *PriorityQueue, roomCap int) { // pq=优先级队列，roomCap=房间人数容量
	// 房间类型
	var gameType int
	if c.lastGameRank == 100 { // 房间类型是普通答题
		gameType = 2
	} else { // 房间类型是排位赛
		gameType = 1
	}
	// 匹配
	if pq.Len() >= roomCap {
		// 创建房间
		room := &Room{
			rank:                     c.lastGameRank,
			clients:                  make(map[*Client]bool),
			questionsAndRightAnswers: make([]QA, 0),
			gameType:                 gameType,
		}
		// 注册用户到房间
		for i := 0; i < roomCap; i++ {
			client := heap.Pop(pq).(*Client)
			client.inGame <- true // 玩家已经进入游戏，通知inGame信道
			client.lastPriorityQueue = pq
			client.room = room
			room.clients[client] = true
		}
		// 查询数据库获取一套题和对应答案,并存入room对象
		room.getQuestionsAndRightAnswers()
		// 注册房间
		c.hub.roomRegister <- room
	} else if *bot {
		go func(c *Client) {
			select {
			case <-time.After(8 * time.Second): // 触发超时，分配一个机器人陪玩家玩耍
				// 创建房间
				room := &Room{
					rank:                     c.lastGameRank,
					clients:                  make(map[*Client]bool),
					questionsAndRightAnswers: make([]QA, 0),
					gameType:                 gameType,
				}
				// 注册用户到房间
				if pq.Len() == 1 {
					client := heap.Pop(pq).(*Client)
					client.inGame <- true // 玩家已经进入游戏，通知inGame信道
					client.lastPriorityQueue = pq
					client.room = room
					room.clients[client] = true
					// 添加robot到房间
					robot := client.newRobot()
					room.clients[robot] = true
					// 查询数据库获取一套题和对应答案,并存入room对象
					room.getQuestionsAndRightAnswers()
					// 注册房间
					client.hub.roomRegister <- room
				}
				return
			case <-c.inGame: // 玩家已经正常匹配成功
				return
			}
		}(c)
	}
}
