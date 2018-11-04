package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bitly/go-simplejson"
	"go.uber.org/zap"
	CO "qa-game/config"
)

const (
	normalTopicNum = 4 // 普通题目数量
	adTopicNum     = 1 // 广告题目数量
)

type AdBuffer struct {
	ads      map[int]*QA  // 所有有效的广告题
	modify   chan int     // adbuf中insert或者update广告题
	delete   chan int     // 广告题消耗次数消耗完，删除adbuf中的广告题，并且持久化数据
	get      chan *Client // 分配一个广告题给玩家，对应adbuf中广告题次数钱数-1
	rollback chan int     // 逃跑玩家不消耗广告题。恢复adbuf中对应广告题次数钱数+1
}

type QA struct {
	Id          int      `json:"-"`           // 题目id
	Title       string   `json:"title"`       // 题目标题
	Options     []string `json:"options"`     // 题目选项
	Answer      int      `json:"-"`           // 题目正确答案
	CorrectRate float64  `json:"-"`           // 题目正答率
	School      string   `json:"school"`      // 题目的类型（科技、历史...）
	Num         int      `json:"num"`         // 第几题
	TotalScore  int      `json:"total_score"` // 一组题的满分分数
	IsAdvert    int      `json:"-"`           // 是否为广告题
	RemainMoney float64  `json:"-"`           // 广告题的剩余广告币
	RemainNum   float64  `json:"-"`           // 广告题的剩余广告次数
	AnswerNum   int      `json:"-"`           // 回答总数
	RightNum    int      `json:"-"`           // 回答正确总数
	Latitude    float64  `json:"-"`           // 广告题的纬度
	Longitude   float64  `json:"-"`           // 广告题的经度
	RangeId     int      `json:"-"`           // 广告题的覆盖范围半径的ID
	RangeValue  int      `json:"-"`           // 广告题的覆盖范围半径（米）
	Money       float64  `json:"-"`           // 广告题一次奖励的钱数
	ImageUrl    string   `json:"-"`           // 题的图片链接
	Geohashs    []string `json:"-"`           // 9个geohash
	Counter     int      `json:"-"`           // 引用计数器
}

var adBuf *AdBuffer // 声明AdBuf

func init() {
	adBuf = &AdBuffer{ // 信道不能加缓冲区，确保adbuf数据的一致性
		ads:      make(map[int]*QA),
		modify:   make(chan int),
		delete:   make(chan int),
		get:      make(chan *Client),
		rollback: make(chan int),
	}
	InitAllAd()
	go AdBufferManager()
}

func InitAllAd() {
	db := CO.DB()
	defer db.Close()

	rows, err := db.Query(fmt.Sprintf("SELECT id,quiz,options,answer,correctrate,school,remain_money,remain_num,answer_num,right_num,latitude,longitude,`range`,aver_money,image_path FROM super_questions WHERE is_advert=1 AND disabled=0"))
	if err != nil {
		Logger.Error("db.Query", zap.NamedError("ctt", err))
	}
	for rows.Next() {
		var id int
		var quiz string
		var optionsChar string
		var options []string
		var answer int
		var correctRate float64
		var schoolId int
		var school string
		var totalScore int
		var remainMoney float64
		var remainNum float64
		var answerNum int
		var rightNum int
		var latitude float64
		var longitude float64
		var rangeId int
		var rangeValue int
		var averMoney float64
		var imageUrl string
		err := rows.Scan(&id, &quiz, &optionsChar, &answer, &correctRate, &schoolId, &remainMoney, &remainNum, &answerNum, &rightNum, &latitude, &longitude, &rangeId, &averMoney, &imageUrl)
		if err != nil {
			Logger.Error("rows.Scan", zap.NamedError("ctt", err))
		}
		// 根据School ID 查询School name
		err = db.QueryRow("SELECT name FROM super_answer WHERE id=?", schoolId).Scan(&school)
		if err != nil {
			Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
		}

		// 根据rangeId 查询rangeValue
		err = db.QueryRow("SELECT `range` FROM super_range WHERE id=?", rangeId).Scan(&rangeValue)
		if err != nil {
			Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
		}

		// 转换options为切片
		optionsChar = optionsChar[2 : len(optionsChar)-2]
		options = strings.Split(optionsChar, "\",\"")

		for _, s := range CO.Base["question_score"].([]int) {
			totalScore += 10 * s
		}
		// 根据坐标计算geohash及其周围一圈的8个geohash
		precision := GetPrecisionByRadius(rangeValue)           // 获取geohash的精度
		geohashs := GetGeohashs(latitude, longitude, precision) // 获取9个geohash

		adBuf.ads[id] = &QA{Id: id, Title: quiz + "(广告题)", Options: options, Answer: answer, CorrectRate: correctRate, School: school, TotalScore: totalScore, IsAdvert: 1, RemainMoney: remainMoney, RemainNum: remainNum, AnswerNum: answerNum, RightNum: rightNum, Latitude: latitude, Longitude: longitude, RangeId: rangeId, RangeValue: rangeValue, Money: averMoney, ImageUrl: imageUrl, Geohashs: geohashs}
	}
}

func (r *Room) getQuestionsAndRightAnswers() {
	var normalTopicCount int
	var players []string // 保存玩家username的切片
	rand.Seed(time.Now().UTC().UnixNano())

	db := CO.DB()
	defer db.Close()
	// Insert房间
	for client := range r.clients {
		if !client.iRobot {
			players = append(players, client.profile.UserName)
		}
	}
	res, err := db.Exec("INSERT INTO super_game_room(players,type,competition_id,w_time) values(?,?,?,?)", strings.Join(players, ","), r.gameType, CO.Base["competition_number"].(int), MakeTimestamp())
	if err != nil {
		Logger.Error("db.Exec", zap.NamedError("ctt", err))
	}
	rid, err := res.LastInsertId()
	if err != nil {
		Logger.Error("res.LastInsertId", zap.NamedError("ctt", err))
	}
	r.Id = int(rid)
	Logger.Info("匹配成功", zap.String("ctt", fmt.Sprintf("[Players]:%v", players)))
	// 广告题，不分配广告题给机器人
	for client := range r.clients {
		if !client.iRobot { // 真人才计算匹配广告题
			adBuf.get <- client
			select {
			case <-client.getAdsDone:
			case <-time.After(time.Second * 20): // 安全起见
				Logger.Warn("获取广告题超时", zap.String("ctt", fmt.Sprintf("玩家username:%s", client.profile.UserName)))
			}
		}
	}
	// 普通题
	err = db.QueryRow("SELECT count(*) FROM super_questions WHERE is_advert=0 AND disabled=0").Scan(&normalTopicCount) // 获取非广告题的总数量
	if err != nil {
		Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
	}
	begin := randInt(0, normalTopicCount-(normalTopicNum+adTopicNum))
	rows, err := db.Query(fmt.Sprintf("SELECT id,quiz,options,answer,school FROM super_questions WHERE is_advert=0 AND disabled=0 LIMIT %d,%d", begin, normalTopicNum+adTopicNum))
	if err != nil {
		Logger.Error("db.Query", zap.NamedError("ctt", err))
	}
	for rows.Next() {
		var id int
		var quiz string
		var optionsChar string
		var options []string
		var answer int
		var schoolId int
		var school string
		var totalScore int
		err := rows.Scan(&id, &quiz, &optionsChar, &answer, &schoolId)
		if err != nil {
			Logger.Error("rows.Scan", zap.NamedError("ctt", err))
		}
		// 根据School ID 查询School name
		err = db.QueryRow("SELECT name FROM super_answer WHERE id=?", schoolId).Scan(&school)
		if err != nil {
			Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
		}

		// 转换options为切片
		optionsChar = optionsChar[2 : len(optionsChar)-2]
		options = strings.Split(optionsChar, "\",\"")

		for _, s := range CO.Base["question_score"].([]int) {
			totalScore += 10 * s
		}

		r.questionsAndRightAnswers = append(r.questionsAndRightAnswers, QA{Id: id, Title: quiz, Options: options, Answer: answer, School: school, TotalScore: totalScore})
	}
}

func modifyOneAd(id int) {
	db := CO.DB()
	defer db.Close()

	rows, err := db.Query(fmt.Sprintf("SELECT id,quiz,options,answer,correctrate,school,disabled,remain_money,remain_num,answer_num,right_num,latitude,longitude,`range`,aver_money,image_path FROM super_questions WHERE id=%d AND is_advert=1", id))
	if err != nil {
		Logger.Error("db.Query", zap.NamedError("ctt", err))
	}
	for rows.Next() {
		var id int
		var quiz string
		var optionsChar string
		var options []string
		var answer int
		var correctRate float64
		var schoolId int
		var school string
		var disabled int
		var totalScore int
		var remainMoney float64
		var remainNum float64
		var answerNum int
		var rightNum int
		var latitude float64
		var longitude float64
		var rangeId int
		var rangeValue int
		var averMoney float64
		var imageUrl string
		err := rows.Scan(&id, &quiz, &optionsChar, &answer, &correctRate, &schoolId, &disabled, &remainMoney, &remainNum, &answerNum, &rightNum, &latitude, &longitude, &rangeId, &averMoney, &imageUrl)
		if err != nil {
			Logger.Error("rows.Scan", zap.NamedError("ctt", err))
		}
		if disabled == 1 { // 禁用了此广告题
			if _, ok := adBuf.ads[id]; ok {
				delete(adBuf.ads, id) // 删除adbuf中此条广告题
				Logger.Info("modifyOneAd", zap.String("ctt", fmt.Sprintf("AdBuffer删除%d成功", id)))
			}
		} else if disabled == 0 { // 新增广告题，或者重新激活此广告题
			// 根据School ID 查询School name
			err = db.QueryRow("SELECT name FROM super_answer WHERE id=?", schoolId).Scan(&school)
			if err != nil {
				Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
			}

			// 根据rangeId 查询rangeValue
			err = db.QueryRow("SELECT `range` FROM super_range WHERE id=?", rangeId).Scan(&rangeValue)
			if err != nil {
				Logger.Error("db.QueryRow", zap.NamedError("ctt", err))
			}

			// 转换options为切片
			optionsChar = optionsChar[2 : len(optionsChar)-2]
			options = strings.Split(optionsChar, "\",\"")

			for _, s := range CO.Base["question_score"].([]int) {
				totalScore += 10 * s
			}
			// 根据坐标计算geohash及其周围一圈的8个geohash
			precision := GetPrecisionByRadius(rangeValue)           // 获取geohash的精度
			geohashs := GetGeohashs(latitude, longitude, precision) // 获取9个geohash
			adBuf.ads[id] = &QA{Id: id, Title: quiz + "(广告题)", Options: options, Answer: answer, CorrectRate: correctRate, School: school, TotalScore: totalScore, IsAdvert: 1, RemainMoney: remainMoney, RemainNum: remainNum, AnswerNum: answerNum, RightNum: rightNum, Latitude: latitude, Longitude: longitude, RangeId: rangeId, RangeValue: rangeValue, Money: averMoney, ImageUrl: imageUrl, Geohashs: geohashs}
			Logger.Info("modifyOneAd", zap.String("ctt", fmt.Sprintf("AdBuffer新增%d成功", id)))
		}
	}
}

func deleteOneAd(id int) {
	// 删除adbuf中id为id的广告题
	if _, ok := adBuf.ads[id]; ok {
		delete(adBuf.ads, id)
	}
	// 数据持久化
	db := CO.DB()
	defer db.Close()
	_, err := db.Exec(`UPDATE super_questions SET disabled=1 WHERE id=?`, id)
	if err != nil {
		Logger.Error("db.Exec", zap.NamedError("ctt", err))
	} else {
		Logger.Info("deleteOneAd", zap.String("ctt", fmt.Sprintf("AdBuffer删除%d成功", id)))
	}
}

// 根据player的geohash随机返回一条精确匹配的广告题
func (c *Client) getOneAd() (*QA, bool) {
	inRangeADs := make(map[int]*QA)    // 粗锁定的广告题
	geoMatchedADs := make(map[int]*QA) // 经纬度精确匹配的广告题
	matchedADs := make(map[int]*QA)    // 去掉已经答过的广告题，最后匹配到的广告题
	for id := range adBuf.ads {
		if adBuf.ads[id].Money >= c.profile.lowestMoney { // 广告题的每次回答的收益钱数必须大于用户设置的最低钱数
			for geohashIndex := range adBuf.ads[id].Geohashs {
				if strings.HasPrefix(c.profile.geohash, adBuf.ads[id].Geohashs[geohashIndex]) {
					inRangeADs[id] = adBuf.ads[id]
					break
				}
			}
		}
	}
	if len(inRangeADs) > 0 {
		for id := range inRangeADs {
			d := distance(location{c.profile.latitude, c.profile.longitude}, location{inRangeADs[id].Latitude, inRangeADs[id].Longitude})
			if d < float64(inRangeADs[id].RangeValue) {
				geoMatchedADs[id] = inRangeADs[id]
			}
		}
		if len(geoMatchedADs) > 0 { // 有至少1个在覆盖范围内的广告题
			usedAdBuf.get <- c
			select {
			case <-c.getUsedAdsDone:
				if c.usedAdQAs == nil { // 如果玩家从来没有玩过广告题
					matchedADs = geoMatchedADs
				} else { // 玩家有玩过的广告题
					for id := range geoMatchedADs { // 遍历匹配geo数据的广告题
						if !IntInSlice(id, c.usedAdQAs) { // 此广告题没有玩过
							matchedADs[id] = geoMatchedADs[id] // 把这个广告题放到最终匹配的广告题列表中
						}
					}
				}
			case <-time.After(time.Second * 20): // 安全起见
				Logger.Warn("获取玩过的广告题超时", zap.String("ctt", fmt.Sprintf("玩家username:%s", c.profile.UserName)))
				matchedADs = geoMatchedADs // 如果获取玩过的广告题超时，就不再筛选已经玩过的广告题了
			}

			if len(matchedADs) > 0 { // 去掉已经玩过的广告题后，有至少1个最终匹配的广告题
				for id := range matchedADs { // 随机取一个广告题，如果这条广告题有效，则减掉对应的消耗，并返回此广告题
					if matchedADs[id].RemainNum >= 1 && matchedADs[id].RemainMoney >= matchedADs[id].Money {
						matchedADs[id].Counter++                           // 引用计数器+1
						matchedADs[id].RemainMoney -= matchedADs[id].Money // 剩余钱数-每次的钱数
						matchedADs[id].RemainNum--                         // 次数-1
						matchedADs[id].AnswerNum++                         // 答题总次数+1
						Logger.Info("getOneAd", zap.String("ctt", fmt.Sprintf("AdBuffer获取%d成功", id)))
						return matchedADs[id], true
					} else if matchedADs[id].Counter == 0 { // 没有引用此广告题的协程，此广告题无效，则从adbuf中删除此广告题
						deleteOneAd(id)
					}
				}
			}
		}
	}
	return nil, false
}

func rollbackOneAd(id int) {
	adBuf.ads[id].Counter--                          // 引用计数器-1
	adBuf.ads[id].RemainMoney += adBuf.ads[id].Money // 剩余钱数+每次的钱数
	adBuf.ads[id].RemainNum++                        // 次数+1
	adBuf.ads[id].AnswerNum--                        // 答题总次数-1
}

// 使用信道处理并发场景下的共享内存数据一致性问题
func AdBufferManager() {
	for {
		select {
		case id := <-adBuf.modify: // 从数据库查询此条广告题，并且insert到adbuf或者update adbuf；类似于数据库的insert和update
			modifyOneAd(id)
		case id := <-adBuf.delete: // 从adbuf中删除ad，并且做数据持久化
			deleteOneAd(id)
		case c := <-adBuf.get: // get adbuf中的ad，为了避免脏读，此处使用了信道；广告题的是否消耗是在最后结算的时候做相应减法，如果中途逃跑则没有消耗此广告题，次数重新+1；此处提前-1是为了解决并发场景下的问题
			if ad, ok := c.getOneAd(); ok {
				c.adQAs = append(c.adQAs, *ad)
			}
			c.getAdsDone <- true
		case id := <-adBuf.rollback: // 玩家中途逃跑，没有回答到此广告题，回滚
			rollbackOneAd(id)
		}
	}
}

func updateAdBuffer(w http.ResponseWriter, r *http.Request) {
	db := CO.DB()
	defer db.Close()

	Logger.Info("updateAdBuffer", zap.String("ctt", "远端IP:"+r.RemoteAddr+" Url:"+r.URL.Path))
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		Logger.Error("ioutil.ReadAll", zap.NamedError("ctt", err))
		http.Error(w, "400 BadRequest", http.StatusBadRequest)
		return
	}
	js, err := simplejson.NewJson(body)
	if err != nil {
		Logger.Error("simplejson.NewJson", zap.NamedError("ctt", err))
		http.Error(w, "400 BadRequest", http.StatusBadRequest)
		return
	}
	clientSign := js.Get("sign").MustString()
	qid := js.Get("qid").MustInt()
	if qid > 0 {
		apiKey := []byte(strconv.Itoa(qid) + CO.APIKEY)
		sign := fmt.Sprintf("%x", md5.Sum(apiKey)) // md5一次
		if clientSign != sign {                    // 验证失败
			Logger.Error("sign Unauthorized", zap.String("ctt", "sign校验失败"))
			http.Error(w, "sign Unauthorized", http.StatusUnauthorized)
			return
		}

		adBuf.modify <- qid
		json.NewEncoder(w).Encode(responseData{Data: "", Msg: "successful", Code: 200, Type: ""})
	} else {
		Logger.Error("400 BadRequest", zap.String("ctt", "无效的qid"))
		http.Error(w, "400 BadRequest", http.StatusBadRequest)
		return
	}
}
