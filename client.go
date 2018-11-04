package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	CO "qa-game/config"
)

const (
	writeWait      = 10 * time.Second    // 写信息给远端的超时时间
	pongWait       = 60 * time.Second    // 读取远端的pong心跳的超时时间
	pingPeriod     = (pongWait * 9) / 10 // 发送ping到远端的时间周期. 必须小于pingpong心跳超时时间
	maxMessageSize = 512                 // 可接受远端的最大的消息长度
)

var (
	newline  = []byte{'\n'}
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		// 允许跨域请求
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

type UserProfile struct {
	Id          int     `json:"user_id"`
	UserName    string  `json:"-"`
	Nickname    string  `json:"nickname"`
	Grade       int     `json:"grade"`
	BrainRank   int     `json:"-"`
	Logo        string  `json:"logo"`
	Guid        string  `json:"-"`
	Continuous  int     `json:"continuous"` // 连续胜利局数
	lowestMoney float64 // 玩家自己设置的参与的最低广告币奖励
	latitude    float64 // 玩家的纬度
	longitude   float64 // 玩家的经度
	geohash     string  // 玩家的比较精确的geohash
}

// 客户端是websocket连接和中心之间的中间人
type Client struct {
	hub               *Hub               // 中心
	room              *Room              // 玩家所在的游戏房间
	lastPriorityQueue *PriorityQueue     // 玩家当前所处的优先级队列
	lastGameRank      int                // 玩家当前所处的优先级队列的id
	answer            map[string]int     // 用户的答案{"题目1"： "A", "题目2": "C"}
	result            map[string]int     // 用户答题的结果{"题目1": true, "题目2": false}
	score             map[string]int     // 用户答题的分数{"题目1": 10, "题目2": 20}
	exp               map[string]int     // 用户答题所得经验值{"题目1": 100, "题目2": 200}
	coin              map[string]float64 // 用户答题所得广告币{"题目1": 1.2, "题目2": 0.5}
	timeLeft          map[string]int     // 用户答题剩下的时间
	totalExp          int                // total_exp
	totalScore        int                // total_score
	totalCoin         float64            // total_coin
	conn              *websocket.Conn    // websocket 连接对象.
	send              chan []byte        // 出站消息的带有缓存的信道
	read              chan []byte        // 进站消息的带有缓存的信道
	escape            bool               // 用户逃跑标识符
	gameover          chan bool          // 游戏结束，通知游戏协程结束
	competitor        *competitor        // 对手信息
	profile           *UserProfile       // 用户信息
	isWinner          bool               // 输赢
	Index             int                // index in pqueue
	inGame            chan bool          // 匹配成功进入游戏，机器人功能专用
	iRobot            bool               // true是机器人，false是真人
	inviterUsername   string             // 好友对战模式，存储邀请人的username
	adQAs             []QA               // 给玩家分配的广告题
	currentTopic      QA                 // 当前进行的题目
	usedAdQAs         []int              // 此玩家答过的广告题的qid
	getAdsDone        chan bool          // 信号，获取完此玩家的广告题
	getUsedAdsDone    chan bool          // 信号，查询完此玩家的所有的使用过的广告题
}

// readPump将websocket连接中的消息泵送到Hub中枢
//
// 每个ws连接中运行一个readPump协程
// 通过在当前协程执行全部的读取动作，确保ws连接上最多只有一个reader
func (c *Client) readPump() {
	defer func() {
		// 已经在游戏中的玩家
		if c.room != nil { // 此玩家逃跑
			c.escape = true
			c.profile.Continuous = 0 // 连胜次数置0
			c.room.runaway = true
			c.hub.roomUnregister <- c.room
		}
		// 注销扳机
		switch c.lastGameRank {
		case 100: // 普通对战模式匹配中的玩家
			c.hub.unregisters[100] <- c
		case 200: // 好友对战模式匹配中的玩家
			c.hub.friendsUnregister <- c
		default:
			if _, ok := CO.Base["question_brain_rank_money"].(map[int]int)[c.lastGameRank]; ok { // 排位赛模式匹配中的玩家
				c.hub.unregisters[c.lastGameRank] <- c
			} else { // 玩家已在进入匹配前退出，玩家没有进入匹配的优先级队列，无需注销
				Logger.Info("readPump", zap.String("ctt", "玩家username:"+c.profile.UserName+" 已在进入匹配前退出"))
			}
		}
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			//if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
			//	Logger.Info("c.conn.ReadMessage", zap.NamedError("ctt", err))
			//}
			Logger.Info("c.conn.ReadMessage", zap.NamedError("ctt", err))
			break
		}
		c.read <- message
	}
}

// writePump将消息从Hub泵送到websocket连接。
//
// 每个ws连接启动一个writePump协程. The
// 通过在当前协程执行全部的写入动作，确保ws连接上最多只有一个writer
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// send信道被关闭
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// 将队列中的聊天消息添加到当前的ws
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// 每个ws连接启动一个prepare协程，用来获取远端的参战类型（段位赛/普通对战/好友对战）
// 然后，注册此连接到hub的对应的信道
func (c *Client) prepare() {
	defer func() { // 安全起见
		if recover() != nil {
			return
		}
	}()

	select {
	case message := <-c.read:
		// 解析message
		js, err := simplejson.NewJson(message)
		if err != nil {
			Logger.Error("simplejson.NewJson", zap.NamedError("ctt", err))
			c.send <- []byte(`{"code": "403", "data": "", "msg": "参数错误", "type": "err"}`)
			//c.conn.Close()
			//close(c.send) // 客户端没有进入注册器，关闭send信道使send协程立即退出以释放资源
			return
		}
		rank := js.Get("rank").MustInt()
		// 校验rank
		if ((rank > (c.profile.BrainRank + 1)) && rank != 100 && rank != 200) || rank <= 0 {
			Logger.Error("rank", zap.String("ctt", "rank不匹配"))
			c.send <- []byte(`{"code": "403", "data": "", "msg": "rank不匹配", "type": "err"}`)
			//c.conn.Close()
			//close(c.send) // 客户端没有进入注册器，关闭send信道使send协程立即退出以释放资源
			return
		}
		Logger.Info("匹配中", zap.String("ctt", fmt.Sprintf("[Username:%s] [Nickname:%s] [Remote IP:%s] [Request Rank:%d] [DB Rank:%d]", c.profile.UserName, c.profile.Nickname, c.conn.RemoteAddr(), rank, c.profile.BrainRank)))
		switch rank {
		case 100: // 注册到普通对战
			c.hub.registers[100] <- c
		case 200: // 注册到好友对战
			c.inviterUsername = js.Get("inviter").MustString() // 解析邀请人inviter:username
			if c.inviterUsername == c.profile.UserName {       // 验证是否自己邀请自己
				c.send <- []byte(`{"code": 403, "data": "", "msg": "自己不能邀请自己", "type": "err"}`)
				//c.conn.Close()
				//close(c.send) // 客户端没有进入注册器，关闭send信道使send协程立即退出以释放资源
				return
			}
			c.hub.friendsRegister <- c
		default: // 注册到排位赛对战
			if _, ok := CO.Base["question_brain_rank_money"].(map[int]int)[rank]; ok {
				c.hub.registers[rank] <- c
			} else {
				c.send <- []byte(`{"code": "403", "data": "", "msg": "参数错误", "type": "err"}`)
				//c.conn.Close()
				//close(c.send) // 客户端没有进入注册器，关闭send信道使send协程立即退出以释放资源
				return
			}
		}
		c.lastGameRank = rank
	case <-time.After(time.Second * 30): // 安全起见，定义一个协程超时时间，如果客户端没有发送数据，30秒后自动退出协程，防止协程泄漏
		Logger.Warn("客户端异常", zap.String("ctt", "客户端异常"))
		c.send <- []byte(`{"code": 403, "data": "", "msg": "客户端异常", "type": "err"}`)
		//c.conn.Close()
		//close(c.send) // 客户端没有进入注册器，关闭send信道使send协程立即退出以释放资源
		return
	}
}

func auth(r *http.Request) (*UserProfile, bool) {
	if username, guid, ok := r.BasicAuth(); ok {
		Logger.Info("auth", zap.String("ctt", "玩家username:"+username+" 正在登录..."))
		// 查询数据库，比对此用户的guid是否相同
		profile := &UserProfile{}
		db := CO.DB()
		defer db.Close()
		// 登录认证
		err := db.QueryRow("SELECT id,username,nickname,logo,guid FROM user WHERE username = ?", username).Scan(&profile.Id, &profile.UserName, &profile.Nickname, &profile.Logo, &profile.Guid)
		if err != nil {
			Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
			Logger.Warn("auth", zap.String("ctt", "玩家username:"+username+" 认证失败"))
			return nil, false
		} else if guid != profile.Guid {
			Logger.Warn("auth", zap.String("ctt", "玩家username:"+username+" guid认证失败"))
			return nil, false
		}
		// 获取玩家信息
		err = db.QueryRow("SELECT brain_rank,continuous,lowest_money,latitude,longitude,user_geohash FROM super_brain WHERE username = ?", username).Scan(&profile.BrainRank, &profile.Continuous, &profile.lowestMoney, &profile.latitude, &profile.longitude, &profile.geohash)
		if err != nil {
			Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
			Logger.Warn("auth", zap.String("ctt", "玩家username:"+username+" 获取信息失败"))
			return nil, false
		} else if profile.latitude == 0.0 || profile.longitude == 0.0 {
			Logger.Warn("auth", zap.String("ctt", "玩家username:"+username+" 获取地理坐标信息失败"))
			return nil, false
		} else {
			Logger.Info("auth", zap.String("ctt", "玩家username:"+username+" 登录成功"))
			return profile, true
		}
	} else {
		Logger.Warn("auth", zap.String("ctt", "request header Invalid"))
		return nil, false
	}
}

// serveWs 处理远端的ws请求.
func serveWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	Logger.Info("serveWs", zap.String("ctt", "远端IP:"+r.RemoteAddr+" Url:"+r.URL.Path))
	// 用户登录认证，通过获取请求头中的认证信息验证用户，验证成功之后升级http协议到ws协议
	if profile, ok := auth(r); ok {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			Logger.Error("upgrader.Upgrade", zap.NamedError("ctt", err))
			return
		}
		client := &Client{
			hub:            hub,
			answer:         make(map[string]int),
			result:         make(map[string]int),
			score:          make(map[string]int),
			exp:            make(map[string]int),
			coin:           make(map[string]float64),
			timeLeft:       make(map[string]int),
			conn:           conn,
			send:           make(chan []byte, 512),
			read:           make(chan []byte, 512),
			gameover:       make(chan bool, 1),
			profile:        profile,
			inGame:         make(chan bool, 1),
			getAdsDone:     make(chan bool, 1),
			getUsedAdsDone: make(chan bool, 1),
		}
		// 通过启动协程，允许函数调用引用内存集合
		go client.writePump()
		go client.readPump()
		go client.prepare()
	} else {
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
	}
}
