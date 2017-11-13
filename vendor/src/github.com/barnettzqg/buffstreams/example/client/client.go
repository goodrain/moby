package main

import (
	"os/signal"
	"strconv"
	"sync"
	"time"

	"os"

	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/barnettzqg/buffstreams"
)

func main() {
	cfg := buffstreams.TCPConnConfig{
		MaxMessageSize: 2048,
		Address:        buffstreams.FormatAddress("127.0.0.1", strconv.Itoa(5031)),
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	n := os.Args[1]
	ni, _ := strconv.Atoi(n)
	logrus.Info("Start thead ", n)
	wait := sync.WaitGroup{}
	stop := make(chan bool)
	for i := 0; i < ni; i++ {
		wait.Add(1)
		go func(in int) {
			btc, err := buffstreams.DialTCP(&cfg)
			if err != nil {
				logrus.Error(err)
				os.Exit(1)
			}
			defer wait.Done()
			for {
				_, err := btc.Write([]byte(fmt.Sprintf("hello word nihao a buhao. %d", in)))
				if err != nil {
					logrus.Error(err)
				}
				select {
				case <-time.Tick(time.Millisecond * 100):
				case <-stop:
					return
				}
			}
		}(i)
	}
	select {
	case <-c:
		close(stop)
	}
	wait.Wait()
}
