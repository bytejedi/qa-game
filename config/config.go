package config

import (
	"log"
	"strconv"
	"strings"

	"github.com/bitly/go-simplejson"
	"github.com/joho/godotenv"
)

const (
	Debug         = false                                                // 是否debug
	APIKEY        = `secret` // apikey
	ROOMCAP       = 2                                                    // 一个游戏房间的玩家数量
	SelfDomain    = "domainname"                                   // 本游戏服务器的域名
	PHPDomain     = "domainname"                                  // php后台域名
	SettlementAPI = "/api/v1/puzzle/fight/Fight"                         // php的结算接口
)

var (
	Base = map[string]interface{}{
		"question_brain_rank_money": "", // 答题对应段位的入场费（单位：广告币）
		"question_exp":              "", // 每道题的经验值
		"question_score":            "", // 题号对应的分数
		"competition_number":        "", // 赛季
		"share_wx_app_bonus":        "", // 分享后获得广告币的比例
	}
)

func init() {
	godotenv.Load()
	loadConf()
}

// Err Log
func Err(err interface{}) {
	if err != nil {
		log.Fatal(err)
	}
}

// load conf from Db
func loadConf() {
	db := DB()
	defer db.Close()
	for field := range Base {
		var data string
		err := db.QueryRow("SELECT data FROM config WHERE name=?", field).Scan(&data)
		Err(err)
		Base[field] = data
	}

	var temQuestionScoreStringSlice []string
	var tmpQuestionScoreIntSlice []int

	temQuestionScoreStringSlice = strings.Split(Base["question_score"].(string), ",")

	for _, v := range temQuestionScoreStringSlice {
		intsoc, err := strconv.Atoi(v)
		Err(err)
		tmpQuestionScoreIntSlice = append(tmpQuestionScoreIntSlice, intsoc)
	}
	Base["question_score"] = tmpQuestionScoreIntSlice
	intsoc, err := strconv.Atoi(Base["question_exp"].(string))
	Err(err)
	Base["question_exp"] = intsoc
	intsoc, err = strconv.Atoi(Base["competition_number"].(string))
	Err(err)
	Base["competition_number"] = intsoc
	intsoc, err = strconv.Atoi(Base["share_wx_app_bonus"].(string))
	Err(err)
	Base["share_wx_app_bonus"] = intsoc
	// 从config表中加载question_brain_rank_money字段，并解析，获得rank对应的入场费
	var rankMoneyMap = make(map[int]int)
	rankMoneyStr := Base["question_brain_rank_money"].(string)
	js, err := simplejson.NewJson([]byte(rankMoneyStr))
	if err != nil {
		Err(err)
	}
	tmpRankMoneyMap, err := js.Map()
	if err != nil {
		Err(err)
	}
	for rank := range tmpRankMoneyMap { // 遍历rankMoneyMap，把rank，money转成int
		k, _ := strconv.Atoi(rank)
		v, _ := strconv.Atoi(tmpRankMoneyMap[rank].(string))
		rankMoneyMap[k] = v
	}
	Base["question_brain_rank_money"] = rankMoneyMap
}
