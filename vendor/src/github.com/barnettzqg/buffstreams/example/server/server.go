package main

import (
	"strconv"

	"github.com/Sirupsen/logrus"
	"github.com/barnettZQG/buffstreams"
)

func main() {
	cfg := buffstreams.TCPListenerConfig{
		EnableLogging:  false,
		MaxMessageSize: 2048,
		Address:        buffstreams.FormatAddress("", strconv.Itoa(5031)),
		Callback: func(me []byte) error {
			logrus.Info(string(me))
			return nil
		},
	}
	btl, err := buffstreams.ListenTCP(cfg)
	if err != nil {
		logrus.Error(err)
	}
	err = btl.StartListening()
	if err != nil {
		logrus.Error(err)
	}
}
