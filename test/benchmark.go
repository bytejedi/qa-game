package test

import (
	"encoding/base64"
	"log"
	"net/url"
	"os"
	"os/signal"

	"fmt"
	"github.com/bitly/go-simplejson"
	"github.com/gorilla/websocket"
	"math/rand"
	"sync"
	"time"
)

var addr = "qa-game.wanxi.mobi"

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func (c *Client) runClient() {
	defer func() {
		c.conn.Close()
		close(c.read)
		close(c.send)
	}()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	var topicTimeout int
	rand.Seed(time.Now().UTC().UnixNano())
	time.Sleep(time.Microsecond * 100)
	c.send <- []byte(`{"rank": 1}`)
	for {
		select {
		case message := <-c.read: // 读取服务端发来的数据
			// 解析message
			js, err := simplejson.NewJson(message)
			if err != nil {
				log.Println("simplejson:", err)
				return
			}
			msgType := js.Get("type").MustString()
			switch msgType {
			case "topic":
				topicTimeout = 0
				time.Sleep(time.Second)
				c.send <- []byte(`{"action":"start"}`) // 发送数据到客户端
			case "ticker":
				if topicTimeout == 0 {
					topicTimeout = js.Get("data").Get("tick").MustInt()
					//time.Sleep(time.Duration(rand.Intn(topicTimeout))*time.Second) // 模拟真人答题耗时
					time.Sleep(time.Duration(rand.Intn(4)) * time.Second) // 模拟真人答题耗时
					option := rand.Intn(4)                                // 模拟真人选择答案，这里随机选择一个选项
					c.send <- []byte(fmt.Sprintf(`{"action":"answer","option":%d}`, option))
				}
			case "total_result", "runaway":
				return
			}
		case <-interrupt:
			return
		}
	}
}

func newClient() *Client {
	u := url.URL{Scheme: "wss", Host: addr, Path: "/"}
	header := map[string][]string{
		"Authorization": {"Basic " + basicAuth("10181319", "fdcea9ec90e5742b31756909c95fa10e")},
		"Origin":        {"https://" + addr},
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		log.Println("dial:", err)
		return nil
	} else {
		client := &Client{
			conn: conn,
			read: make(chan []byte, 512),
			send: make(chan []byte, 512),
		}
		go client.readPump()
		go client.writePump()
		return client
	}
}

func Run() {
	gos := 200
	log.SetFlags(log.Lmicroseconds | log.Lshortfile | log.LstdFlags)
	for {
		var waitGroup sync.WaitGroup
		startTime := time.Now()
		for i := 0; i < gos; i++ {
			waitGroup.Add(1)
			log.Println("启动第", i, "个客户端")
			go func() {
				client := newClient()
				client.runClient()
				waitGroup.Done()
			}()
		}
		elapsed := time.Since(startTime)
		fmt.Println("启动:", gos, "个客户端，总耗时:", elapsed)
		waitGroup.Wait()
	}
}
