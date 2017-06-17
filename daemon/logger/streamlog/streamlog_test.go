package streamlog

import (
	"testing"
	"time"

	"sync"

	"fmt"

	"github.com/docker/docker/daemon/logger"
	"github.com/pborman/uuid"
)

func TestStreamLog(t *testing.T) {
	log, err := New(logger.Context{
		ContainerID:  uuid.New(),
		ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
		Config:       map[string]string{"stream-server": "127.0.0.1:6362"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wait := sync.WaitGroup{}
	for i := 0; i < 50; i++ {
		wait.Add(1)
		go func() {
			for j := 0; j < 1000; j++ {
				log.Log(&logger.Message{
					Line:      []byte(fmt.Sprintf("hello word %d", j)),
					Timestamp: time.Now(),
					Source:    "stdout",
				})
				time.Sleep(time.Millisecond * 100)
			}
			wait.Done()
		}()
	}
	wait.Wait()
}

func BenchmarkStreamLog(t *testing.B) {
	log, err := New(logger.Context{
		ContainerID:  uuid.New(),
		ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
		Config:       map[string]string{"stream-server": "127.0.0.1:5031"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < t.N; i++ {
		log.Log(&logger.Message{
			Line:      []byte("hello word"),
			Timestamp: time.Now(),
			Source:    "stdout",
		})
	}
}
