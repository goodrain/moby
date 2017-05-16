package zmqlog

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"encoding/json"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/logger"
	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

const (
	name       = "zmqlog"
	zmqAddress = "zmq-address"
)

type ZmqLogger struct {
	writer      *zmq.Socket
	stopChan    chan bool
	containerID string
	tenantID    string
	serviceID   string
	monitorID   string
	ctx         logger.Context
	felock      sync.Mutex
}

func init() {
	if err := logger.RegisterLogDriver(name, New); err != nil {
		logrus.Fatal(err)
	}
	if err := logger.RegisterLogOptValidator(name, ValidateLogOpt); err != nil {
		logrus.Fatal(err)
	}
}

var defaultClusterAddress = "http://region.goodrain.me:6363/docker-instance"
var defaultAddress = "tcp://region.goodrain.me:6362"

func New(ctx logger.Context) (logger.Logger, error) {
	var (
		env       = make(map[string]string)
		tenantId  string
		serviceId string
	)
	for _, pair := range ctx.ContainerEnv {
		p := strings.SplitN(pair, "=", 2)
		//logrus.Errorf("ContainerEnv pair: %s", pair)
		if len(p) == 2 {
			key := p[0]
			value := p[1]
			env[key] = value
		}
	}
	tenantId = env["TENANT_ID"]
	serviceId = env["SERVICE_ID"]

	var logAddress string
	if zmqaddress, ok := ctx.Config[zmqAddress]; !ok {
		logAddress = GetLogAddress(serviceId)
		logrus.Infof("get a log server address %s", logAddress)
	} else {
		logAddress = zmqaddress
	}

	puber, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		return nil, err
	}
	uuid := uuid.New()

	puber.Monitor(fmt.Sprintf("inproc://%s.rep", uuid), zmq.EVENT_ALL)

	if tenantId == "" {
		tenantId = "default"
	}

	if serviceId == "" {
		serviceId = "default"
	}

	err = puber.Connect(logAddress)
	if err != nil {
		return nil, err
	}

	logger := &ZmqLogger{
		writer:      puber,
		containerID: ctx.ID(),
		tenantID:    tenantId,
		serviceID:   serviceId,
		felock:      sync.Mutex{},
		monitorID:   uuid,
		stopChan:    make(chan bool),
		ctx:         ctx,
	}
	go logger.monitor()
	return logger, nil
}

func (s *ZmqLogger) Log(msg *logger.Message) error {
	s.felock.Lock()
	defer s.felock.Unlock()
	s.writer.Send(s.serviceID, zmq.SNDMORE)
	if msg.Source == "stderr" {
		s.writer.Send(s.containerID+": "+string(msg.Line), zmq.DONTWAIT)
	} else {
		s.writer.Send(s.containerID+": "+string(msg.Line), zmq.DONTWAIT)
	}
	return nil
}

func (s *ZmqLogger) Close() error {
	s.felock.Lock()
	defer s.felock.Unlock()
	close(s.stopChan)
	if s.writer != nil {
		return s.writer.Close()
	}
	return nil
}

func (s *ZmqLogger) Name() string {
	return name
}

func (s *ZmqLogger) monitor() {
	mo, _ := zmq.NewSocket(zmq.PAIR)
	mo.Connect(fmt.Sprintf("inproc://%s.rep", s.monitorID))
	var retry int
	for {
		select {
		case <-s.stopChan:
			return
		case <-time.Tick(time.Millisecond * 100):
		}
		event, _, _, err := mo.RecvEvent(0)
		if err != nil {
			logrus.Error("Zmq Logger monitor zmq connection error.", err)
			continue
		}
		if event.String() == "EVENT_CLOSED" {
			retry++
			if retry > 120 { //每秒2次，重试2分钟，120次
				s.reConnect()
				return
			}
		}
		if event.String() == "EVENT_CONNECTED" {
			retry = 0
		}
	}
}

func (s *ZmqLogger) reConnect() {
	logrus.Info("Zmq Logger start reConnect zmq server.")
	var logAddress string
	if zmqaddress, ok := s.ctx.Config[zmqAddress]; !ok {
		logAddress = GetLogAddress(s.serviceID)
	} else {
		logAddress = zmqaddress
	}

	puber, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		logrus.Error("ReConnect create socket error.", err.Error())
		return
	}
	err = puber.Connect(logAddress)
	if err != nil {
		logrus.Errorf("ReConnect socket connect %s error. %s", logAddress, err.Error())
		return
	}
	uuid := uuid.New()
	puber.Monitor(fmt.Sprintf("inproc://%s.rep", uuid), zmq.EVENT_ALL)
	s.monitorID = uuid
	s.writer = puber
	go s.monitor()
}

//ValidateLogOpt 参数检测
func ValidateLogOpt(cfg map[string]string) error {
	for key := range cfg {
		switch key {
		case zmqAddress:
		default:
			return fmt.Errorf("unknown log opt '%s' for %s log driver", key, name)
		}
	}
	// zmqAddress不需要强制设置
	// if cfg[zmqAddress] == "" {
	// 	return fmt.Errorf("must specify a value for log opt '%s'", zmqAddress)
	// }
	return nil
}

// GetLogAddress 动态获取日志服务端地址
func GetLogAddress(serviceID string) string {
	var clusterAddress []string

	//res, err := http.DefaultClient.Get("http://region.goodrain.me:8888/v1/etcd/event-log/instances")
	res, err := http.DefaultClient.Get("http://test.goodrain.com:8888/v1/etcd/event-log/instances")
	if err != nil {
		logrus.Errorf("Error get docker log instance from region api: %v", err)
		clusterAddress = append(clusterAddress, defaultClusterAddress)
	}
	var instances = struct {
		Data struct {
			Instance []struct {
				HostIP  string
				WebPort int
			} `json:"instance"`
		} `json:"data"`
		OK bool `json:"ok"`
	}{}
	err = json.NewDecoder(res.Body).Decode(&instances)
	if err != nil {
		logrus.Errorf("Error Decode instance info: %v", err)
		clusterAddress = append(clusterAddress, defaultClusterAddress)
	}
	res.Body.Close()
	if len(instances.Data.Instance) > 0 {
		for _, ins := range instances.Data.Instance {
			if ins.HostIP != "" && ins.WebPort != 0 {
				clusterAddress = append(clusterAddress, fmt.Sprintf("http://%s:%d/docker-instance?service_id=%s", ins.HostIP, ins.WebPort, serviceID))
			}
		}
	}
	if len(clusterAddress) < 1 {
		clusterAddress = append(clusterAddress, defaultClusterAddress)
	}
	for _, address := range clusterAddress {
		res, err := http.DefaultClient.Get(address)
		if err != nil {
			continue
		}
		var host = make(map[string]string)
		err = json.NewDecoder(res.Body).Decode(&host)
		if err != nil {
			logrus.Errorf("Error Decode BEST instance host info: %v", err)
			continue
		}
		res.Body.Close()
		if status, ok := host["status"]; ok && status == "success" {
			return host["host"]
		}
	}
	return defaultAddress
}
