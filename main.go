package main

import (
	"flag"
	"net/http"

	"fmt"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
	CO "qa-game/config"
)

var addr = flag.String("addr", "0.0.0.0:443", "Https service address")
var bot = flag.Bool("bot", false, "With robot service")
var verbosity = flag.Int("verbosity", 3, "Logging verbosity: 0=debug, 1=error, 2=warn, 3=info (default: 3)")

func main() {
	flag.Parse()
	InitLogger(*verbosity)
	hub := newHub()
	go hub.run()
	go hub.runRoomManager()
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/api/v1/AdBuffer/", updateAdBuffer).Methods("POST")
	router.HandleFunc("/api/v1/ZhangHui/", restartServer).Methods("POST")
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	}).Methods("GET")
	err := http.ListenAndServeTLS(*addr, fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", CO.SelfDomain), fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", CO.SelfDomain), router)
	//err := http.ListenAndServe(*addr, router)
	if err != nil {
		Logger.Fatal("ListenAndServe", zap.NamedError("ctt", err))
	}
}
