package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/bitly/go-simplejson"
	"go.uber.org/zap"
)

type wxUserProfile struct {
	Nickname string
	Logo     string
}

var wxUsers = [...]wxUserProfile{
	{"iron", "http://wx.qlogo.cn/mmopen/vi_32/DYAIOgq83erALNpuZFB4a3wlIyy4XpVe6uLPJOcUhN6gQ7tbeicHbPcqiccLMG7QJbKdWUeKzKVhZYIg5jYpeOpg/0"},
	{"BigBrother临", "https://wx.qlogo.cn/mmopen/vi_32/DYAIOgq83eqomylXeibuWQ4gOfafFxauiaHZYyNFic2Uocmp9ulCARvQNweYLncYPicC4P4FzQqtorFlv1sEffqqfA/0"},
	{"猫小萌", "http://wx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTKHK3HvrhljlS86ZUyzjyN6O4727fxy8sDDA9icWRCPg770KXx1GyvBicPC4EcXA8uwkxTsZYA3TeRg/0"},
	{"24K纯帅", "https://wx.qlogo.cn/mmopen/vi_32/J63VlI2ibnQibWk62u4nyO1Fj5u705ZuyX1jbCJLv9p6msyoibqfyMDUlzAbXVSOJiaN9UTKoOeJgcVgMot9obz6hg/0"},
	{"喜欢小草莓", "https://wx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTLns60AgRWAiaRZGMEb5vhefvQicKhXX4SUyiccMPNs77TufyusBIbFK0GE10y3DeWYYQJibBknCicDsmg/0"},
	{"hd", "https://wx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTIjHnUTMRMZ9W5HMaGkROFgiaBEbwxNDpuBCdxnOqBialzYLecpevDlibIZ479RlYzqxxfic5pmXQf4hg/0"},
	{"冯大师专业吃鸡", "https://wx.qlogo.cn/mmopen/vi_32/Q0j4TwGTfTJRicbeBb6nzJkO7ibuC4ribyAicLjfxyN3KjeWKYydAEwqYh11CyFwLWtTWic8iaNpDIxOibWicYkuYaZJhw/0"},
}

// new 机器人
func (c *Client) newRobot() *Client {
	rand.Seed(time.Now().UTC().UnixNano())
	wxUserProfile := wxUsers[rand.Intn(len(wxUsers))]
	return &Client{
		hub:      c.hub,
		answer:   make(map[string]int),
		result:   make(map[string]int),
		score:    make(map[string]int),
		exp:      make(map[string]int),
		coin:     make(map[string]float64),
		timeLeft: make(map[string]int),
		send:     make(chan []byte, 512),
		read:     make(chan []byte, 512),
		gameover: make(chan bool, 1),
		profile: &UserProfile{
			Id:         rand.Intn(1000000),
			UserName:   "I am robot",
			Nickname:   wxUserProfile.Nickname,
			Grade:      c.profile.Grade,
			BrainRank:  c.profile.BrainRank,
			Logo:       wxUserProfile.Logo,
			Continuous: 0,
		},
		inGame: make(chan bool, 1),
		iRobot: true,
	}
}

// 机器人客户端，模拟真人玩游戏的逻辑
func (c *Client) runRobot() {
	var topicTimeout int
	rand.Seed(time.Now().UTC().UnixNano())
	for {
		select {
		case message := <-c.send: // 读取服务端发来的数据
			// 解析message
			js, err := simplejson.NewJson(message)
			if err != nil {
				Logger.Error("simplejson.NewJson", zap.NamedError("ctt", err))
				return
			}
			msgType := js.Get("type").MustString()
			switch msgType {
			case "topic":
				topicTimeout = 0
				time.Sleep(time.Second)
				c.read <- []byte(`{"action":"start"}`) // 发送数据到客户端
			case "ticker":
				if topicTimeout == 0 {
					topicTimeout = js.Get("data").Get("tick").MustInt()
					//time.Sleep(time.Duration(rand.Intn(topicTimeout))*time.Second) // 模拟真人答题耗时
					time.Sleep(time.Duration(rand.Intn(4)) * time.Second) // 模拟真人答题耗时
					option := rand.Intn(4)                                // 模拟真人选择答案，这里随机选择一个选项
					c.read <- []byte(fmt.Sprintf(`{"action":"answer","option":%d}`, option))
				}
			case "total_result", "runaway":
				return
			}
		case <-c.gameover:
			return
		}
	}
}
