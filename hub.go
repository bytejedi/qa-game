package main

import (
	"container/heap"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bitly/go-simplejson"
	"go.uber.org/zap"
	CO "qa-game/config"
)

// Hub 维护一组活的客户端并向该客户端广播消息，而且维护一组活跃的房间并向房间内的客户端广播消息
type Hub struct {
	pqs               map[int]*PriorityQueue // 排位赛模式下的优先级队列，key=玩家进入的排位赛(段位)ID
	registers         map[int]chan *Client   // 排位赛模式下的玩家注册器，key=玩家进入的排位赛(段位)ID
	unregisters       map[int]chan *Client   // 排位赛模式下的玩家注销器，key=玩家进入的排位赛(段位)ID
	friends           map[string]*Client     // 好友对战玩家 key=username
	friendsRegister   chan *Client           // 好友对战注册器
	friendsUnregister chan *Client           // 好友对战注销器
	rooms             map[*Room]bool         // 游戏室
	roomRegister      chan *Room             // 房间注册器
	roomUnregister    chan *Room             // 房间注销器
	hubBroadcast      chan []byte            // 全网广播信道
}

type Room struct {
	Id                       int              // room id
	rank                     int              // rank
	clients                  map[*Client]bool // room的client
	questionsAndRightAnswers []QA             // room的普通题目和对应的正确答案(数量=普通题数+广告题数)
	runaway                  bool             // 有玩家逃跑
	gameType                 int              // 房间类型，1：排位赛2：普通答题3：好友对战
}

// 一道题的结果对应json的结构
type topicResultData struct {
	CAnswer []map[string]int `json:"c_answer"`
	TAnswer int              `json:"t_answer"`
}

// 结算结果对应json的结构
type resultData struct {
	Users      []map[string]interface{} `json:"users"`
	Rid        int                      `json:"rid"`          // 此局游戏的房间号
	AdImageUrl string                   `json:"ad_image_url"` // 广告题的图片url
}

// 定义返回消息（对手）结构体
type competitor struct {
	Logo     string `json:"logo"`     // 对手头像url "http://abc.com/tom.png"
	Nickname string `json:"nickname"` // 对手昵称 "Tom"
	Grade    string `json:"grade"`    // 对手的等级 "1"
}

type responseData struct {
	Data interface{} `json:"data"`
	Msg  string      `json:"msg"`
	Code int         `json:"code"`
	Type string      `json:"type"`
}

func newHub() *Hub {
	hub := &Hub{
		pqs:               make(map[int]*PriorityQueue),
		registers:         make(map[int]chan *Client),
		unregisters:       make(map[int]chan *Client),
		hubBroadcast:      make(chan []byte),
		friends:           make(map[string]*Client),
		friendsRegister:   make(chan *Client),
		friendsUnregister: make(chan *Client),
		rooms:             make(map[*Room]bool),
		roomRegister:      make(chan *Room),
		roomUnregister:    make(chan *Room),
	}
	for rank := range CO.Base["question_brain_rank_money"].(map[int]int) { // 创建各排位赛的优先级队列，玩家注册、注销信道
		hub.pqs[rank] = NewPriorityQueue(1000)     // 优先级队列，初始化优先级队列的容量为1000个元素，提高性能
		hub.registers[rank] = make(chan *Client)   // 玩家注册信道
		hub.unregisters[rank] = make(chan *Client) // 玩家注销信道
	}
	hub.pqs[100] = NewPriorityQueue(1000)     // 创建普通对战模式的优先级队列，普通对战的rank是100，初始化优先级队列的容量为1000个元素，提高性能
	hub.registers[100] = make(chan *Client)   // 普通对战玩家注册信道
	hub.unregisters[100] = make(chan *Client) // 普通对战玩家注销信道
	return hub
}

// 处理ws连接的枢纽，处理用户各游戏模式的注册注销(排位赛，普通对战，好友对战)以及全网广播
func (h *Hub) run() {
	// 排位赛和普通对战模式
	for rank := range h.pqs {
		go func(h *Hub, rank int) {
			for {
				select {
				case client := <-h.registers[rank]: // 注册，注册到玩家优先级队列
					heap.Push(h.pqs[rank], client)
					client.match(h.pqs[rank], CO.ROOMCAP)
				case client := <-h.unregisters[rank]: // 注销，从优先级队列注销玩家
					// 将 client 从优先级队列中删除
					if h.pqs[rank].Exists(client) {
						heap.Remove(h.pqs[rank], client.Index)
					}
					Logger.Info("已注销", zap.String("ctt", fmt.Sprintf("[Username:%s] [Nickname:%s] [Index:%d] [Rank1 Lenght:%d] [Last Rank:%d]", client.profile.UserName, client.profile.Nickname, client.Index, h.pqs[rank].Len(), client.lastGameRank)))
					close(client.send) // 关闭send信道，send协程退出，防止协程泄漏
				}
			}
		}(h, rank)
	}
	// 好友对战模式，好友对战比较特殊，使用的map，不需要使用优先级队列
	go func(h *Hub) {
		for {
			select {
			case client := <-h.friendsRegister:
				h.friends[client.profile.UserName] = client
				client.matchInvite()
			case client := <-h.friendsUnregister:
				if _, ok := h.friends[client.profile.UserName]; ok {
					delete(h.friends, client.profile.UserName)
				}
				Logger.Info("已注销", zap.String("ctt", fmt.Sprintf("[Username:%s] [Nickname:%s] [Index:%d] [Rank200 Lenght:%d] [Last Rank:%d]", client.profile.UserName, client.profile.Nickname, client.Index, len(h.friends), client.lastGameRank)))
				close(client.send) // 关闭send信道，send协程退出，防止协程泄漏
			}
		}
	}(h)
	// 全网广播
	//go func(h *Hub) {
	//	for {
	//		select {
	//		case message := <-h.hubBroadcast:
	//			for i := range h.rank100Clients {
	//				select {
	//				case h.rank100Clients[i].send <- message:
	//				default:
	//					close(h.rank100Clients[i].send)
	//					heap.Remove(&h.rank100Clients, i)
	//				}
	//			}
	//		}
	//	}
	//}(h)
}

// 处理房间注册注销
func (h *Hub) runRoomManager() {
	// 房间注册注销
	for {
		select {
		// 注册房间
		case room := <-h.roomRegister:
			h.rooms[room] = true
			// 启动游戏
			go room.startGame()
		// 注销房间
		case room := <-h.roomUnregister:
			if _, ok := h.rooms[room]; ok {
				delete(h.rooms, room)
				// 结算
				go room.settlement()
			}
		}
	}
}

// 结算
func (r *Room) settlement() {
	db := CO.DB()
	defer db.Close()
	// 本局游戏数据入库前的一些前期处理，包括广告题内存回滚，清理机器人，结束玩家协程
	for client := range r.clients {
		client.room = nil
		// 通知game协程退出
		client.gameover <- true
		if !client.iRobot { // 我是真人
			if client.escape { // 逃跑
				client.coin = make(map[string]float64) // 清空广告题所得广告币收益
				// 玩家逃跑，则不管答没答到广告题，都把广告题回滚
				for i := range client.adQAs {
					adBuf.rollback <- client.adQAs[i].Id
				}
			} else { // 没逃跑
				// 如果对手逃跑之前本玩家没有答到广告题，则把事先分配的广告题回滚
				for i := range client.adQAs {
					if _, ok := client.answer[client.adQAs[i].Title]; !ok { // client的answer里没有此广告题，说明玩家没有回答到此广告题
						adBuf.rollback <- client.adQAs[i].Id // 回滚此广告题
					}
				}
			}
		} else { // 我是机器人
			if _, ok := r.clients[client]; ok { // 删除房间中的机器人，防止机器人数据入库
				delete(r.clients, client)
			}
		}
	}
	// 游戏结果数据入库(先入库，再返回前端结算结果，保证入库数据和展示数据一致性，避免结算结果没有入库成功就返回前端)
	var totalResultMessage []byte
	var rData resultData
	var res responseData
	// 入库
	err := r.insertGameData()
	if err != nil { // 入库失败
		Logger.Error("游戏结果入库失败", zap.NamedError("ctt", err))
		res = responseData{Data: "", Msg: "数据入库失败", Code: 500, Type: "total_result"}
	} else { // 入库成功
		go httpPost(r.Id, r.rank) // 请求php结算接口
		Logger.Info("游戏结果入库成功", zap.String("ctt", "游戏结果入库成功"))
		// 修改usedAdBuf，合成total_result数据
		for c := range r.clients {
			if c.adQAs != nil {
				usedAdBuf.add <- c
			}
			rData.Users = append(rData.Users, map[string]interface{}{"user_id": c.profile.Id, "continuous": c.profile.Continuous, "coin": c.totalCoin, "exp": c.totalExp, "total_score": c.totalScore})
		}
		rData.Rid = r.Id
		rData.AdImageUrl = r.questionsAndRightAnswers[len(r.questionsAndRightAnswers)-1].ImageUrl
		res = responseData{Data: rData, Msg: "", Code: 200, Type: "total_result"}
	}
	//判断房间内是否有逃跑的玩家
	if r.runaway { // 如果房间有其他玩家逃跑
		res.Msg = "对方已逃跑"
	}

	// 序列化本局游戏最终结果
	totalResultMessage, err = json.Marshal(res)
	if err != nil {
		Logger.Error("json.Marshal", zap.NamedError("ctt", err))
	}

	// 广播整局游戏的答题结果
	for c := range r.clients {
		if !c.iRobot && !c.escape { // 不是机器人并且没逃跑
			Logger.Info("发送给真人的本局结果", zap.String("ctt", string(totalResultMessage)))
			c.send <- totalResultMessage
		}
	}
}

// 整局游戏结果信息入库
func (r *Room) insertGameData() error {
	db := CO.DB()
	defer db.Close()
	// Begin函数内部会去获取连接，开启事务
	tx, err := db.Begin()
	if err != nil {
		Logger.Error("开启事务失败", zap.NamedError("ctt", err))
		CO.TxRollback(tx)
		return err
	}
	// 更新玩家的连胜次数
	for c := range r.clients {
		_, err := tx.Exec("UPDATE super_brain SET continuous=? WHERE user_id=?", c.profile.Continuous, c.profile.Id)
		if err != nil {
			Logger.Error("tx.Exec", zap.NamedError("ctt", err))
			CO.TxRollback(tx)
			return err
		}
	}
	// 插入super_game_log表
	for c := range r.clients {
		var escape = 0
		if c.escape {
			escape = 1
		}

		allQAs := r.questionsAndRightAnswers // 把默认的房间的以广告题+普通题总数为长度的普通题切片赋值给临时变量
		if len(c.adQAs) > 0 {                // 如果此玩家分配到广告题，则用广告题替换默认的普通题
			for i := range c.adQAs {
				allQAs[i+normalTopicNum] = c.adQAs[i]
			}
		}

		for _, t := range allQAs { // 普通题+广告题
			if _, ok := c.answer[t.Title]; ok { // 此玩家已回答的题
				t.AnswerNum++      // 此题目的被回答总数+1
				_, err := tx.Exec( // 入super_game_log表
					"INSERT INTO super_game_log(rid,username,qid,answer,result,usetime,timeleft,score,exp,w_time,escape,coin) values(?,?,?,?,?,?,?,?,?,?,?,?)",
					r.Id,
					c.profile.UserName,
					t.Id,
					c.answer[t.Title],
					c.result[t.Title],
					10-c.timeLeft[t.Title],
					c.timeLeft[t.Title],
					c.score[t.Title],
					c.exp[t.Title],
					MakeTimestamp(),
					escape,
					c.coin[t.Title],
				)
				if err != nil {
					Logger.Error("tx.Exec", zap.NamedError("ctt", err))
					CO.TxRollback(tx)
					return err
				}
				if !c.escape { // 没有逃跑
					if t.IsAdvert == 1 { // 此玩家没有逃跑，并且此题是广告题
						if c.result[t.Title] == 1 { // 回答正确
							t.RightNum++
						}
						t.CorrectRate = Round(float64(t.RightNum)/float64(t.AnswerNum), 2) // 计算正答率
						_, err := tx.Exec(                                                 // 数据持久化，update super_questions表
							"UPDATE super_questions SET correctrate=?,remain_money=?,remain_num=?,answer_num=?,right_num=? WHERE id=?",
							t.CorrectRate,
							t.RemainMoney,
							t.RemainNum,
							t.AnswerNum,
							t.RightNum,
							t.Id,
						)
						if err != nil {
							Logger.Error("tx.Exec", zap.NamedError("ctt", err))
							CO.TxRollback(tx)
							return err
						}
					} else if t.IsAdvert == 0 { // 此玩家没有逃跑，并且此题是 普通题
						if c.result[t.Title] == 1 { // 回答正确
							t.RightNum++
						}
						t.CorrectRate = Round(float64(t.RightNum)/float64(t.AnswerNum), 2) // 计算正答率
						_, err := tx.Exec(                                                 // 数据持久化，update super_questions表
							"UPDATE super_questions SET correctrate=?,answer_num=?,right_num=? WHERE id=?",
							t.CorrectRate,
							t.AnswerNum,
							t.RightNum,
							t.Id,
						)
						if err != nil {
							Logger.Error("tx.Exec", zap.NamedError("ctt", err))
							CO.TxRollback(tx)
							return err
						}
					}
				}
			}
		}
	}
	//最后释放tx内部的连接
	err = tx.Commit()
	if err != nil {
		Logger.Error("tx.Commit", zap.NamedError("ctt", err))
		CO.TxRollback(tx)
		return err
	}
	return nil
}

// 封装post请求
func httpPost(rid, rank int) {
	url := fmt.Sprintf("https://%s%s?room_id=%d&rank=%d", CO.PHPDomain, CO.SettlementAPI, rid, rank)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader("name=abc"))
	if err != nil {
		Logger.Error("http.Post", zap.NamedError("ctt", err))
	}

	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			Logger.Error("ioutil.ReadAll", zap.NamedError("ctt", err))
		}
		Logger.Info("结算接口", zap.String("ctt", string(body)))

		Logger.Info("结算完成", zap.String("ctt", fmt.Sprintf("[RoomID:%d]", rid)))
	}
}

func (r *Room) startGame() {
	//defer func(){ // 必须要先声明defer，否则不能捕获到panic异常
	//	fmt.Println("c")
	//	if err:=recover();err!=nil{
	//		fmt.Println(err) // 这里的err其实就是panic传入的内容
	//	}
	//	fmt.Println("d")
	//}()

	if *bot {
		for c := range r.clients {
			if c.iRobot {
				go c.runRobot() // 启动机器人
			}
		}
	}

	// 发送对手个人信息
	for client := range r.clients {
		for competitor := range r.clients {
			if client != competitor {
				// 合成对手信息json
				res := responseData{Data: competitor.profile, Msg: "", Code: 200, Type: "competitor_info"}
				b, err := json.Marshal(res)
				if err != nil {
					Logger.Error("json.Marshal", zap.NamedError("ctt", err))
				}
				// 发送对手信息
				if r.runaway {
					return
				}
				client.send <- b
			}
		}
	}

	// 开始游戏逻辑
	for i, topic := range r.questionsAndRightAnswers {
		isSameTopic := true // 玩家的题目是否相同，1.普通题都相同，广告题不同2.玩家a是广告题，玩家b是普通题
		// 序列化题目和选项
		// 发题前sleep 1秒，前端播放动画
		time.Sleep(time.Second)
		if i < normalTopicNum { // 广播发送普通题，普通题都是一样的
			topic.Num = i + 1 // 第几道题
			res := responseData{Data: topic, Msg: "", Code: 200, Type: "topic"}
			b, err := json.Marshal(res)
			if err != nil {
				Logger.Error("json.Marshal", zap.NamedError("ctt", err))
			}
			// 发题目和选项
			for c := range r.clients {
				c.currentTopic = topic
				if r.runaway {
					return
				}
				c.send <- b
			}
		} else { // 分别发送每个玩家对应的广告题
			for c := range r.clients {
				if len(c.adQAs) > 0 { // 如果有对应此玩家的广告题
					c.currentTopic = c.adQAs[i-normalTopicNum]
				} else { // 没有对应此玩家的广告题
					c.currentTopic = topic
				}
				c.currentTopic.Num = i + 1 // 第几道题
				res := responseData{Data: c.currentTopic, Msg: "", Code: 200, Type: "topic"}
				b, err := json.Marshal(res)
				if err != nil {
					Logger.Error("json.Marshal", zap.NamedError("ctt", err))
				}
				if r.runaway {
					return
				}
				c.send <- b
			}
			isSameTopic = false
		}

		// 等待客户端start
		if !r.waitStart() {
			return
		}

		// 启动一道题
		if value := r.gameLevel(isSameTopic); !value {
			return
		}
	}
	// 判定输赢
	r.judgment()

	// 注销房间，注销房间会触发结算
	for c := range r.clients {
		c.hub.roomUnregister <- r
		break
	}
	// 再来一局游戏，复用websocket，初始化client数据
	//client.competitor = nil
	//for k := range client.answer {
	//	delete(client.answer, k)
	//	delete(client.exp, k)
	//	delete(client.result, k)
	//	delete(client.timeLeft, k)
	//	delete(client.score, k)
	//}
	//go client.prepare() // 不阻塞其他玩家
}

// 房间广播
func (r *Room) broadcast(message []byte) {
	for c := range r.clients {
		// 广播发送一个消息给所有用户
		if r.runaway {
			return
		}
		c.send <- message
	}
}

// 等待客户端start
func (r *Room) waitStart() bool {
	var ret = true
	// 声明协程同步变量
	var waitGroup sync.WaitGroup
	for c := range r.clients {
		// 同步协程数+1
		waitGroup.Add(1)
		go func(c *Client) {
			select {
			// 接收客户端的消息
			case message := <-c.read:
				// 解析message
				js, err := simplejson.NewJson(message)
				if err != nil {
					Logger.Error("simplejson.NewJson", zap.NamedError("ctt", err))
					c.send <- []byte(`{"code": 403, "data": "", "msg": "start参数错误", "type": "err"}`)
					//c.conn.Close()
					ret = false
				} else {
					if action := js.Get("action").MustString(); action != "start" {
						Logger.Error("action", zap.String("ctt", "start不匹配"))
						c.send <- []byte(`{"code": 403, "data": "", "msg": "start不匹配", "type": "err"}`)
						//c.conn.Close()
						ret = false
					}
				}
			// 游戏结束
			case <-c.gameover:
				ret = false
			// 设置协程超时时间，防止协程泄漏
			case <-time.After(time.Second * 30):
				Logger.Warn("客户端异常", zap.String("ctt", "客户端异常"))
				c.send <- []byte(`{"code": 403, "data": "", "msg": "客户端异常", "type": "err"}`)
				//c.conn.Close()
				ret = false
			}
			waitGroup.Done()
		}(c)
	}
	// 阻塞主业务逻辑，等待多个协程结束
	waitGroup.Wait()
	return ret
}

// 游戏逻辑，一道题是一个业务(一个游戏关卡)
func (r *Room) gameLevel(isSameTopic bool) bool {
	//defer func() {
	//
	//}()

	var ret = true

	// 声明协程同步变量
	var waitGroup sync.WaitGroup
	// 声明控制ticker关闭
	ctx := context.Background()
	ctx, cancelAllTicker := context.WithCancel(ctx)

	// 监听此房间中所有用户的回复消息
	for c := range r.clients {
		// 同步协程数+1
		waitGroup.Add(1)

		// 启动timer
		timeout := make(chan bool, 1)

		go func(c *Client, ctx context.Context) {
			for i := 10; i >= 0; i-- {
				select {
				case <-ctx.Done():
					return
				default:
					c.timeLeft[c.currentTopic.Title] = i
					c.send <- []byte(fmt.Sprintf("{\"code\": 200, \"data\": {\"tick\": %d}, \"msg\": \"\", \"type\": \"ticker\"}", i))
					time.Sleep(time.Second) //sleep 1 second
				}
			}
			timeout <- true
		}(c, ctx)
		// 启动监听协程，获取当前用户的答案等状态
		go func(c *Client) {
			Logger.Info("开始答题", zap.String("ctt", fmt.Sprintf("[Username:%s] [Nickname:%s] [RoomID:%d]", c.profile.UserName, c.profile.Nickname, r.Id)))
			select {
			// 游戏结束
			case <-c.gameover:
				ret = false
			// 用户已回复答案
			case message := <-c.read:
				// 解析客户端发来的答案json,把用户的答案存到用户对象
				js, err := simplejson.NewJson(message)
				if err != nil {
					Logger.Error("simplejson.NewJson", zap.NamedError("ctt", err))
					c.send <- []byte(`{"code": 403, "data": "", "msg": "解析玩家答案出错", "type": "err"}`)
					//c.conn.Close()
					ret = false
				} else {
					if action := js.Get("action").MustString(); action != "answer" {
						Logger.Error("action", zap.String("ctt", "玩家答案action出错"))
						c.send <- []byte(`{"code": 403, "data": "", "msg": "玩家答案action出错", "type": "err"}`)
						//c.conn.Close()
						ret = false
					} else {
						option := js.Get("option").MustInt()
						Logger.Info("FromClient", zap.String("ctt", fmt.Sprintf("[Username:%s] [Nickname:%s] [RoomID:%d] [玩家选项:%d] [正确答案:%d] [剩余时间:%d]", c.profile.UserName, c.profile.Nickname, r.Id, option+1, c.currentTopic.Answer, c.timeLeft[c.currentTopic.Title])))
						c.answer[c.currentTopic.Title] = option + 1
						// 查询答题是否正确
						if c.currentTopic.Answer == c.answer[c.currentTopic.Title] { // 回答正确
							c.result[c.currentTopic.Title] = 1                                                                                         // 回答正确的标识符1
							c.score[c.currentTopic.Title] = CO.Base["question_score"].([]int)[c.currentTopic.Num-1] * c.timeLeft[c.currentTopic.Title] // 计算这道题的得分
							c.exp[c.currentTopic.Title] = CO.Base["question_exp"].(int)
							c.totalScore += c.score[c.currentTopic.Title]
							c.totalExp += c.exp[c.currentTopic.Title]
							if c.currentTopic.IsAdvert == 1 { // 如果是广告题
								c.coin[c.currentTopic.Title] = c.currentTopic.Money * (float64(100-CO.Base["share_wx_app_bonus"].(int)) / 100.0)
								c.totalCoin += c.coin[c.currentTopic.Title]
							}
						} else { // 回答错误
							if c.currentTopic.IsAdvert == 1 { // 如果是广告题
								c.coin[c.currentTopic.Title] = c.currentTopic.Money * (float64(100-CO.Base["share_wx_app_bonus"].(int)) / 100.0) / 2
								c.totalCoin += c.coin[c.currentTopic.Title]
							}
							c.result[c.currentTopic.Title] = 0 // 回答错误的标识符0
						}
						// 对房间进行广播，返回玩家得分
						for client := range r.clients {
							client.send <- []byte(fmt.Sprintf("{\"code\": 200, \"data\": {\"user_id\": %d,\"nickname\": \"%s\", \"score\": %d}, \"msg\": \"\", \"type\": \"score\"}", c.profile.Id, c.profile.Nickname, c.score[c.currentTopic.Title]))
						}
					}
				}

			// 用户答题超时
			case <-timeout:
				c.answer[c.currentTopic.Title] = 0 // 超时没有回答
				c.result[c.currentTopic.Title] = 0 // 超时的标识符跟回答错误一样也是0
				c.score[c.currentTopic.Title] = 0  // 计算这道题的得分
				c.exp[c.currentTopic.Title] = 0    // 计算这道题的所得经验
				if c.currentTopic.IsAdvert == 1 {  // 如果是广告题
					c.coin[c.currentTopic.Title] = c.currentTopic.Money * (float64(100-CO.Base["share_wx_app_bonus"].(int)) / 100.0) / 2
					c.totalCoin += c.coin[c.currentTopic.Title]
				}
			}
			// 此道题目结束，同步协程数-1
			waitGroup.Done()
		}(c)
	}

	// 阻塞主业务逻辑，等待多个协程结束
	waitGroup.Wait()
	// 关闭所有ticker协程
	cancelAllTicker()
	// 如果游戏中途结束，则不发送单道题的结果给客户端，因为客户端的channel已经关闭了
	if ret {
		r.topicResult(isSameTopic) // 所有用户答题结束，房间广播，发送房间内所有玩家的答题结果
	}
	return ret
}

func (r *Room) topicResult(isSameTopic bool) {
	var allTData topicResultData       // topic_result JSON对象
	time.Sleep(time.Millisecond * 100) // sleep 0.1 second，发太快的话前端会认为是一条消息
	if isSameTopic {                   // 如果题目相同
		for c := range r.clients {
			allTData.CAnswer = append(allTData.CAnswer, map[string]int{"user_id": c.profile.Id, "answer": c.answer[c.currentTopic.Title] - 1})
			allTData.TAnswer = c.currentTopic.Answer - 1 // 正确答案
		}

		res := responseData{Data: allTData, Msg: "", Code: 200, Type: "topic_result"}
		b, err := json.Marshal(res)
		if err != nil {
			Logger.Error("json.Marshal", zap.NamedError("ctt", err))
		}
		// 房间广播
		r.broadcast(b)
	} else { // 题目不相同
		for c := range r.clients {
			for other := range r.clients {
				if c != other { // 其他玩家
					if other.currentTopic.Answer == other.answer[other.currentTopic.Title] { // 如果其他玩家回答正确
						allTData.CAnswer = append(allTData.CAnswer, map[string]int{"user_id": other.profile.Id, "answer": c.answer[c.currentTopic.Title] - 1})
					} else { // 其他玩家other回答错误，那么随机生成一个非c玩家题目正确答案的选项
						var randAnswer int // 随机选项
						for {
							randAnswer = randInt(1, 5)               // 从1开始到5（不包含5）
							if randAnswer != c.currentTopic.Answer { // 随机选项不等于c玩家的正确答案
								break
							}
						}
						allTData.CAnswer = append(allTData.CAnswer, map[string]int{"user_id": other.profile.Id, "answer": randAnswer - 1})
					}
				} else { // 此玩家自己
					allTData.CAnswer = append(allTData.CAnswer, map[string]int{"user_id": c.profile.Id, "answer": c.answer[c.currentTopic.Title] - 1})
				}
			}

			allTData.TAnswer = c.currentTopic.Answer - 1 // 正确答案

			// 序列化
			res := responseData{Data: allTData, Msg: "", Code: 200, Type: "topic_result"}
			b, err := json.Marshal(res)
			if err != nil {
				Logger.Error("json.Marshal", zap.NamedError("ctt", err))
			}
			// 发送topic_result
			c.send <- b
		}
	}
}

func (r *Room) judgment() { // 判定输赢；修改赢家的isWinner为true
	var clientSlice ClientSlice
	for c := range r.clients {
		clientSlice = append(clientSlice, c)
	}
	sort.Sort(clientSlice)                                      // 分数降序排序
	if clientSlice[0].totalScore != clientSlice[1].totalScore { // 赢家出现
		clientSlice[0].profile.Continuous++ // 赢家连胜次数+1
		for _, c := range clientSlice[1:] {
			c.profile.Continuous = 0 // 除赢家外，其他玩家continuous都置0
		}
	} else { // 平手
		for _, c := range clientSlice {
			c.profile.Continuous = 0 // 所有玩家continuous都置0
		}
	}
}
