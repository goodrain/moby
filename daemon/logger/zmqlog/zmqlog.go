package zmqlog

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"encoding/json"

	"time"

	"bytes"

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
	zmqCtx      *zmq.Context
	buf         *bytes.Buffer
}

func init() {
	if err := logger.RegisterLogDriver(name, New); err != nil {
		logrus.Fatal(err)
	}
	if err := logger.RegisterLogOptValidator(name, ValidateLogOpt); err != nil {
		logrus.Fatal(err)
	}
	zmq.SetMaxSockets(5000)
}

var defaultClusterAddress = "http://127.0.0.1:6363/docker-instance"
var defaultAddress = "tcp://127.0.0.1:6362"

//New 创建
func New(ctx logger.Context) (logger.Logger, error) {
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
		logrus.Infof("get a log server address %s", logAddress)
	} else {
		logAddress = zmqaddress
	}
	var puber *zmq.Socket
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
		buf:         bytes.NewBuffer(nil),
	}
	//fmt.Printf("init zmq ctx: %p \n", logger.zmqCtx)
	go logger.monitor()
	return logger, nil
}

//Log 发送
func (s *ZmqLogger) Log(msg *logger.Message) error {
	s.felock.Lock()
	defer s.felock.Unlock()
	s.buf.WriteString(`{"container_id":"`)
	s.buf.WriteString(s.containerID)
	s.buf.WriteString(`","msg":`)
	ffjsonWriteJSONBytesAsString(s.buf, msg.Line)
	s.buf.WriteString(`,"time":"`)
	s.buf.WriteString(msg.Timestamp.Format(time.RFC3339))
	s.buf.WriteString(`","service_id":"`)
	s.buf.WriteString(s.serviceID)
	s.buf.WriteString(`"}`)
	stream := s.buf.Bytes()
	defer s.buf.Reset()
	_, err := s.writer.SendBytes(stream, zmq.DONTWAIT)
	if err != nil {
		logrus.Error("Log Send error:", err.Error())
	}
	return nil
}

//Close 关闭
func (s *ZmqLogger) Close() error {
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
	return nil
}

//Name 返回name
func (s *ZmqLogger) Name() string {
	return name
}

func (s *ZmqLogger) monitor() {
	var mo *zmq.Socket
	var poller *zmq.Poller
	var err error
	mo, err = zmq.NewSocket(zmq.PAIR)
	if err != nil {
		logrus.Errorf("Service %s zmq logger monitor create error. %s", s.serviceID, err.Error())
		return
	}
	defer func() {
		logrus.Info("closed monitor zmq socket.")
		//mo.Close()
	}()
	err = mo.Connect(fmt.Sprintf("inproc://%s.rep", s.monitorID))
	if err != nil {
		logrus.Errorf("Service %s zmq loggermonitor connect error. %s", s.serviceID, err.Error())
		return
	}
	var retry int
	poller = zmq.NewPoller()
	poller.Add(mo, zmq.POLLIN)
	for !s.stop {
		sockets, _ := poller.Poll(time.Second * 1)
		for _, socket := range sockets {
			switch soc := socket.Socket; soc {
			case mo:
				event, _, _, err := mo.RecvEvent(0)
				if err != nil {
					logrus.Warningf("Service %s zmq Logger monitor zmq connection error. %s", s.serviceID, err)
					return
				}
				if event == zmq.EVENT_CLOSED {
					retry++
					if retry > 60 { //每秒2次，重试30s，60次
						var logAddress string
						if zmqaddress, ok := s.ctx.Config[zmqAddress]; !ok {
							logAddress = GetLogAddress(s.serviceID)
						} else {
							logAddress = zmqaddress
						}
						// 若地址未改变，不进行重联
						if logAddress == s.logAddress {
							logrus.Infof("Service %s zmq Logger retry get address,but not change.", s.serviceID)
							retry = 0
						} else {
							go s.reConnect(logAddress)
							return
						}
					}
				}
				if event == zmq.EVENT_CONNECTED {
					retry = 0
				}
			}
		}
	}
	mo.Close()
}

func (s *ZmqLogger) reConnect(logAddress string) error {
	s.felock.Lock()
	defer s.felock.Unlock()
	logrus.Info("Zmq Logger start reConnect zmq server:", logAddress)
	var err error
	//必须设置，否则zmqCtx.Term()会阻塞
	s.writer.SetLinger(0)
	err = s.writer.Close()
	if err != nil {
		logrus.Errorf("service %s before Recreate zmq socket close old socket error. %s", s.serviceID, err)
	}

	s.writer, err = zmq.NewSocket(zmq.PUB)
	if err != nil {
		logrus.Errorf("service %s Recreate zmq socket error. %s", s.serviceID, err)
		return err
	}
	s.logAddress = logAddress
	err = s.writer.Connect(logAddress)
	if err != nil {
		logrus.Errorf("service %s Recreate zmq socket error. %s", s.serviceID, err)
		return err
	}
	uuid := uuid.New()
	s.monitorID = uuid
	//fmt.Printf("recreate zmq ctx: %p \n", s.zmqCtx)
	s.writer.Monitor(fmt.Sprintf("inproc://%s.rep", s.monitorID), zmq.EVENT_ALL)
	go s.monitor()
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
	return nil
}

// GetLogAddress 动态获取日志服务端地址
func GetLogAddress(serviceID string) string {
	var clusterAddress []string
	res, err := http.DefaultClient.Get("http://127.0.0.1:8888/v1/etcd/event-log/instances")
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
	if res != nil && res.Body != nil {
		defer res.Body.Close()
		err = json.NewDecoder(res.Body).Decode(&instances)
		if err != nil {
			logrus.Errorf("Error Decode instance info: %v", err)
			clusterAddress = append(clusterAddress, defaultClusterAddress)
		}
		if len(instances.Data.Instance) > 0 {
			for _, ins := range instances.Data.Instance {
				if ins.HostIP != "" && ins.WebPort != 0 {
					clusterAddress = append(clusterAddress, fmt.Sprintf("http://%s:%d/docker-instance?&mode=zmq&service_id=%s", ins.HostIP, ins.WebPort, serviceID))
				}
			}
		}
		if len(clusterAddress) < 1 {
			clusterAddress = append(clusterAddress, defaultClusterAddress+"?mode=zmq&service_id="+serviceID)
		}
	}

	return getLogAddress(clusterAddress)
}

func getLogAddress(clusterAddress []string) string {
	for _, address := range clusterAddress {
		res, err := http.DefaultClient.Get(address)
		if res != nil && res.Body != nil {
			defer res.Body.Close()
		}
		if err != nil {
			logrus.Warningf("Error get host info from %s. %s", address, err)
			continue
		}
		var host = make(map[string]string)
		err = json.NewDecoder(res.Body).Decode(&host)
		if err != nil {
			logrus.Errorf("Error Decode BEST instance host info: %v", err)
			continue
		}
		if status, ok := host["status"]; ok && status == "success" {
			return host["host"]
		}
	}
	return defaultAddress
}
func ffjsonWriteJSONBytesAsString(buf *bytes.Buffer, s []byte) {
	const hex = "0123456789abcdef"

	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if 0x20 <= b && b != '\\' && b != '"' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}
			if start < i {
				buf.Write(s[start:i])
			}
			switch b {
			case '\\', '"':
				buf.WriteByte('\\')
				buf.WriteByte(b)
			case '\n':
				buf.WriteByte('\\')
				buf.WriteByte('n')
			case '\r':
				buf.WriteByte('\\')
				buf.WriteByte('r')
			default:

				buf.WriteString(`\u00`)
				buf.WriteByte(hex[b>>4])
				buf.WriteByte(hex[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRune(s[i:])
		if c == utf8.RuneError && size == 1 {
			if start < i {
				buf.Write(s[start:i])
			}
			buf.WriteString(`\ufffd`)
			i += size
			start = i
			continue
		}

		if c == '\u2028' || c == '\u2029' {
			if start < i {
				buf.Write(s[start:i])
			}
			buf.WriteString(`\u202`)
			buf.WriteByte(hex[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		buf.Write(s[start:])
	}
	buf.WriteByte('"')
}
