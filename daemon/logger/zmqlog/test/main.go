package main

import (
	"net/http"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/logger"
	"github.com/docker/docker/daemon/logger/zmqlog"
	"github.com/pborman/uuid"
)

func main() {
	http.HandleFunc("/start", start)
	http.HandleFunc("/stop", stop)
	logrus.Info("start listen")
	err := http.ListenAndServe("0.0.0.0:10001", nil)
	if err != nil {
		logrus.Error("websocket listen error.", err.Error())
	}
}

var cache = make(map[string]chan struct{})
var cacheLog = make(map[string]logger.Logger)

func start(w http.ResponseWriter, r *http.Request) {
	log, err := zmqlog.New(logger.Context{
		ContainerID:  uuid.New(),
		ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
		Config:       map[string]string{"zmq-address": "tcp://region.goodrain.me:6362"},
	})
	if err != nil {
		logrus.Fatal(err)
	}
	stop := make(chan struct{})
	go send(log, stop)
	cache[r.FormValue("key")] = stop
	cacheLog[r.FormValue("key")] = log
	logrus.Info("Start a log")
}
func stop(w http.ResponseWriter, r *http.Request) {
	if ch, ok := cache[r.FormValue("key")]; ok {
		close(ch)
	}
	if log, ok := cacheLog[r.FormValue("key")]; ok {
		log.Close()
	}
	logrus.Info("close a log")
}

var message = `
time="2017-05-26T22:22:33+08:00" level=info msg="ServiceID:bf6acf27af857e14049ee2cfe2a1707c" module=SocketServer
time="2017-05-26T22:23:01+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:23:11+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:23:21+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:23:31+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:23:41+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:23:51+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:24:01+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:24:11+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:24:21+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:24:31+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:24:41+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:24:51+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:25:01+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
time="2017-05-26T22:25:11+08:00" level=error msg="Store manager docker message chan cache size to achieve perfection" module=MessageStore
`

func send(log logger.Logger, stop chan struct{}) {
	for {
		err := log.Log(&logger.Message{
			Line:      []byte(message),
			Timestamp: time.Now(),
			Source:    "stdout",
		})
		if err != nil {
			logrus.Fatal(err)
		}
		select {
		case <-stop:
			return
		case <-time.Tick(time.Millisecond * 5):
		}
	}
}
