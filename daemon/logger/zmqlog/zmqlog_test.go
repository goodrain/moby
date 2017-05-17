package zmqlog

import (
	"testing"
	"time"

	"github.com/docker/docker/daemon/logger"
	"github.com/pborman/uuid"
)

func TestZMQLog(t *testing.T) {
	log, err := New(logger.Context{
		ContainerID:  uuid.New(),
		ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for i := 0; i < 10000; i++ {
		log.Log(&logger.Message{
			Line:      []byte("hello word"),
			Timestamp: time.Now(),
			Source:    "stdout",
		})
		time.Sleep(time.Millisecond * 100)
	}
}

func TestGetLogAddress(t *testing.T) {
	adress := getLogAddress([]string{"http://127.0.0.1:2003/docker-instance?service_id=123", "http://127.0.0.1:6363/docker-instance?service_id=123"})
	t.Log(adress)
}
