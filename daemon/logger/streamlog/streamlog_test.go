package streamlog

import (
	"bufio"
	"io"
	"os"
	"testing"
	"time"

	"sync"

	"fmt"

	"github.com/docker/docker/daemon/logger"
	"github.com/pborman/uuid"
)

func TestStreamLog(t *testing.T) {

	wait := sync.WaitGroup{}
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			log, err := New(logger.Context{
				ContainerID:  uuid.New(),
				ContainerEnv: []string{"TENANT_ID=" + uuid.New(), "SERVICE_ID=" + uuid.New()},
				Config:       map[string]string{"stream-server": "127.0.0.1:6362"},
			})
			if err != nil {
				t.Fatal(err)
			}
			fi, err := os.Open("./log.txt")
			if err != nil {
				fmt.Printf("Error: %s\n", err)
				return
			}
			defer fi.Close()

			br := bufio.NewReader(fi)
			for {
				a, _, c := br.ReadLine()
				if c == io.EOF {
					break
				}
				log.Log(&logger.Message{
					Line:      []byte(a),
					Timestamp: time.Now(),
					Source:    "stdout",
				})
			}
			wait.Done()
		}()
	}
	wait.Wait()
	time.Sleep(20 * time.Second)
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
