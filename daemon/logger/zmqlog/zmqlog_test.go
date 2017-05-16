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
	for i := 0; i < 1000; i++ {
		log.Log(&logger.Message{
			Line:      []byte("hello word"),
			Timestamp: time.Now(),
			Source:    "stdout",
		})
		time.Sleep(time.Millisecond * 10)
		t.Log("Send a message .")
	}
}
