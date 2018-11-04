package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"github.com/bitly/go-simplejson"
	"go.uber.org/zap"
	"io/ioutil"
	"net/http"
	"os"
	CO "qa-game/config"
)

// 注意！：此功能必须配合systemd使用，并且unit内添加Restart=on-failure选项，否则无效
func restartServer(w http.ResponseWriter, r *http.Request) {
	Logger.Info("restartServer", zap.String("ctt", "远端IP:"+r.RemoteAddr+" Url:"+r.URL.Path))
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
	orderId := js.Get("order_id").MustString()
	apiKey := []byte(orderId + CO.APIKEY)
	sign := fmt.Sprintf("%x", md5.Sum(apiKey)) // md5一次
	if clientSign != sign {                    // 验证失败
		Logger.Error("sign Unauthorized", zap.String("ctt", "sign校验失败"))
		http.Error(w, "sign Unauthorized", http.StatusUnauthorized)
		return
	} else {
		json.NewEncoder(w).Encode(responseData{Data: "", Msg: "successful", Code: 200, Type: ""})
		os.Exit(1)
	}
}
