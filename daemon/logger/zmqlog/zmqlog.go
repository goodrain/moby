package zmqlog

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

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
	stop        bool
	containerID string
	tenantID    string
	serviceID   string
	monitorID   string
	ctx         logger.Context
	felock      sync.Mutex
	logAddress  string
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
var i int

//New 创建
func New(ctx logger.Context) (logger.Logger, error) {
	i++
	logrus.Infof("New a zmq logger.%d", i)
	var (
		env       = make(map[string]string)
		tenantID  string
		serviceID string
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
	tenantID = env["TENANT_ID"]
	serviceID = env["SERVICE_ID"]
	if tenantID == "" {
		tenantID = "default"
	}

	if serviceID == "" {
		serviceID = "default"
	}

	var logAddress string
	if zmqaddress, ok := ctx.Config[zmqAddress]; !ok {
		logAddress = GetLogAddress(serviceID)
		logrus.Debugf("get a log server address %s", logAddress)
	} else {
		logAddress = zmqaddress
	}
	puber, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		return nil, err
	}
	err = puber.Connect(logAddress)
	if err != nil {
		return nil, err
	}

	uuid := uuid.New()
	puber.Monitor(fmt.Sprintf("inproc://%s.rep", uuid), zmq.EVENT_ALL)

	logger := &ZmqLogger{
		writer:      puber,
		containerID: ctx.ID(),
		tenantID:    tenantID,
		serviceID:   serviceID,
		felock:      sync.Mutex{},
		monitorID:   uuid,
		ctx:         ctx,
		logAddress:  logAddress,
	}
	go logger.monitor()
	return logger, nil
}

//Log 发送
func (s *ZmqLogger) Log(msg *logger.Message) error {
	s.felock.Lock()
	defer s.felock.Unlock()
	_, err := s.writer.Send(s.serviceID, zmq.SNDMORE)
	if err != nil {
		logrus.Error("Log Send error:", err.Error())
	}
	if msg.Source == "stderr" {
		_, err = s.writer.Send(s.containerID+": "+string(msg.Line), zmq.DONTWAIT)
	} else {
		_, err = s.writer.Send(s.containerID+": "+string(msg.Line), zmq.DONTWAIT)
	}
	if err != nil {
		logrus.Error("Log Send error:", err.Error())
	}
	return nil
}

var closed int

//Close 关闭
func (s *ZmqLogger) Close() error {
	closed++
	logrus.Infof("ZMQ Logger Closing. %d", closed)
	s.felock.Lock()
	defer s.felock.Unlock()
	s.stop = true
	if s.writer != nil {
		s.writer.SetLinger(10)
		err := s.writer.Close()
		if err != nil {
			return err
		}
	}
	logrus.Infof("ZMQ Logger Closed. %d", closed)
	return nil
}

//Name 返回name
func (s *ZmqLogger) Name() string {
	return name
}

func (s *ZmqLogger) monitor() {
	mo, _ := zmq.NewSocket(zmq.PAIR)
	defer func() {
		mo.SetLinger(0)
		mo.Close()
	}()
	mo.Connect(fmt.Sprintf("inproc://%s.rep", s.monitorID))
	var retry int

	poller := zmq.NewPoller()
	poller.Add(mo, zmq.POLLIN)
	for !s.stop {
		sockets, _ := poller.Poll(-1)
		for _, socket := range sockets {
			switch soc := socket.Socket; soc {
			case mo:
				event, _, _, err := mo.RecvEvent(0)
				if err != nil {
					logrus.Warning("Zmq Logger monitor zmq connection error.", err)
					return
				}
				if event.String() == "EVENT_CLOSED" {
					retry++
					if retry > 60 { //每秒2次，重试30s，60次
						if err := s.reConnect(); err == nil {
							retry = 0
						}
					}
				}
				if event.String() == "EVENT_CONNECTED" {
					retry = 0
				}
			}
		}
	}
	logrus.Infof("zmq socket monitor closed. %d", closed)
}

func (s *ZmqLogger) reConnect() error {
	var logAddress string
	if zmqaddress, ok := s.ctx.Config[zmqAddress]; !ok {
		logAddress = GetLogAddress(s.serviceID)
	} else {
		logAddress = zmqaddress
	}
	logrus.Info("Zmq Logger start reConnect zmq server:", logAddress)
	s.felock.Lock()
	defer s.felock.Unlock()
	err := s.writer.Disconnect(s.logAddress)
	if err != nil {
		logrus.Errorf("ReConnect socket Disconnect %s error. %s", logAddress, err.Error())
		return err
	}
	err = s.writer.Connect(logAddress)
	if err != nil {
		logrus.Errorf("ReConnect socket connect %s error. %s", logAddress, err.Error())
		return err
	}
	s.logAddress = logAddress
	return nil
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
	res, err := http.DefaultClient.Get("http://region.goodrain.me:8888/v1/etcd/event-log/instances")
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
	return getLogAddress(clusterAddress)
}

func getLogAddress(clusterAddress []string) string {
	for _, address := range clusterAddress {
		res, err := http.DefaultClient.Get(address)
		if err != nil {
			logrus.Warning("Error get host info from " + address)
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
