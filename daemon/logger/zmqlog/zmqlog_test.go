package zmqlog

import (
	"testing"
	"time"

	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/logger"
	"github.com/pborman/uuid"
)

func TestZMQLog(t *testing.T) {
	t.SkipNow()
	log, err := New(logger.Context{
		ContainerID:  uuid.New(),
		ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
		Config:       map[string]string{"zmq-address": "tcp://127.0.0.1:6362"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	err = log.Log(&logger.Message{
		Line:      []byte("hello word"),
		Timestamp: time.Now(),
		Source:    "stdout",
	})
	if err != nil {
		t.Fatal(err)
	}
	logrus.Info("Send message")
}

func TestZMQLogNew(b *testing.T) {
	wait := sync.WaitGroup{}
	for i := 0; i < 5; i++ {
		log, err := New(logger.Context{
			ContainerID:  uuid.New(),
			ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
			Config:       map[string]string{"zmq-address": "tcp://127.0.0.1:6362"},
		})
		if err != nil {
			b.Fatal(err)
		}
		wait.Add(1)
		go func(log logger.Logger) {
			for j := 0; j < 10000; j++ {
				err = log.Log(&logger.Message{
					Line:      []byte("hello word"),
					Timestamp: time.Now(),
					Source:    "stdout",
				})
				if err != nil {
					b.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			log.Close()
			wait.Done()
			logrus.Info("发送完毕")
		}(log)
		time.Sleep(10 * time.Millisecond)
	}
	wait.Wait()
	time.Sleep(time.Minute * 1)
}

func TestGetLogAddress(t *testing.T) {
	t.SkipNow()
	adress := getLogAddress([]string{"http://127.0.0.1:2003/docker-instance?service_id=123", "http://127.0.0.1:6363/docker-instance?service_id=123"})
	t.Log(adress)
}
