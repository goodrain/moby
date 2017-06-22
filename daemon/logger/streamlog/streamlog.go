package streamlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/context"

	"strconv"

	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/logger"
)

//STREAMLOGNAME 插件名称
const name = "streamlog"
const defaultClusterAddress = "http://127.0.0.1:6363/docker-instance"
const defaultAddress = "127.0.0.1:6362"

func init() {
	if err := logger.RegisterLogDriver(name, New); err != nil {
		logrus.Fatal(err)
	}
	logrus.Info("streamlog driver register success.")
	if err := logger.RegisterLogOptValidator(name, ValidateLogOpt); err != nil {
		logrus.Fatal(err)
	}
}

//StreamLog 消息流log
type StreamLog struct {
	writer        *Client
	serviceID     string
	tenantID      string
	containerID   string
	errorQueue    [][]byte
	reConnecting  chan bool
	serverAddress string
	ctx           context.Context
	cancel        context.CancelFunc
	cacheSize     int
	cacheQueue    chan string
	lock          sync.Mutex
	config        map[string]string
	size          int
}

//New new logger
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
	address := getTCPConnConfig(serviceID, ctx.Config["stream-server"])
	writer, err := NewClient(address)
	if err != nil {
		return nil, err
	}

	cacheSize, err := strconv.Atoi(ctx.Config["cache-log-size"])
	if err != nil {
		cacheSize = 1024
	}
	currentCtx, cancel := context.WithCancel(context.Background())
	logger := &StreamLog{
		writer:        writer,
		serviceID:     serviceID,
		tenantID:      tenantID,
		containerID:   ctx.ContainerID,
		ctx:           currentCtx,
		cancel:        cancel,
		cacheSize:     cacheSize,
		config:        ctx.Config,
		serverAddress: address,
		reConnecting:  make(chan bool, 1),
		cacheQueue:    make(chan string, 5000),
	}
	err = writer.Dial()
	if err != nil {
		logrus.Error("connect log server error.log can not be sended.")
		go logger.reConect()
	} else {
		logrus.Info("stream log server is connected")
	}
	go logger.send()
	return logger, nil
}

func getTCPConnConfig(serviceID, address string) string {
	if address == "" {
		address = GetLogAddress(serviceID)
	}
	if strings.HasPrefix(address, "tcp://") {
		address = address[6:]
	}
	return address
}

//ValidateLogOpt 验证参数
func ValidateLogOpt(cfg map[string]string) error {
	for key, value := range cfg {
		switch key {
		case "stream-server":
		case "cache-error-log-size":
			if _, err := strconv.Atoi(value); err != nil {
				return errors.New("cache error log size must be a number")
			}
		default:
			return fmt.Errorf("unknown log opt '%s' for %s log driver", key, name)
		}
	}
	return nil
}

func (s *StreamLog) cache(msg string) {
	defer func() {
		recover()
	}()
	select {
	case s.cacheQueue <- msg:
	default:
		return
	}
}

func (s *StreamLog) send() {
	logrus.Info("Start to send")
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-s.cacheQueue:
			if msg == "" {
				continue
			}
			time.Sleep(time.Microsecond * 50)
			if !s.writer.IsClosed() {
				err := s.writer.Write(msg)
				if err != nil {
					logrus.Error("send log message to stream server error.", err.Error())
					s.cache(msg)
					if isConnectionClosed(err) && len(s.reConnecting) < 1 {
						s.reConect()
					}
				} else {
					s.size++
				}
			} else {
				logrus.Error("the writer is closed.try reconect")
				if len(s.reConnecting) < 1 {
					s.reConect()
				}
			}
		}
	}
}

//Log log
func (s *StreamLog) Log(msg *logger.Message) error {
	defer func() { //必须要先声明defer，否则不能捕获到panic异常
		if err := recover(); err != nil {
			logrus.Error("Stream log pinic.", err)
		}
	}()
	buf := bytes.NewBuffer(nil)
	buf.WriteString(s.containerID[0:12] + ",")
	buf.WriteString(s.serviceID)
	buf.WriteString(string(msg.Line))
	s.cache(buf.String())
	return nil
}

func isConnectionClosed(err error) bool {
	if err == errClosed || err == errNoConnect {
		return true
	}
	errMsg := err.Error()
	ok := strings.HasSuffix(errMsg, "connection refused") || strings.HasSuffix(errMsg, "use of closed network connection")
	if !ok {
		return strings.HasSuffix(errMsg, "broken pipe")
	}
	return ok
}

func (s *StreamLog) reConect() {
	s.reConnecting <- true
	defer func() { <-s.reConnecting }()
	for {
		logrus.Info("start reconnect stream log server.")
		//step1 try reconnect current address
		if s.writer != nil {
			err := s.writer.ReConnect()
			if err == nil {
				return
			}
		}
		//step2 get new server address and reconnect
		server := getTCPConnConfig(s.serviceID, s.config["stream-server"])
		if server == s.serverAddress {
			logrus.Warning("stream log server address not change ,will reconnect")
			err := s.writer.ReConnect()
			if err != nil {
				logrus.Error("stream log server connect error." + err.Error())
			} else {
				return
			}
		} else {
			err := s.writer.ChangeAddress(server)
			if err != nil {
				logrus.Errorf("stream log server connect %s error. %v", server, err.Error())
			} else {
				return
			}
		}

		select {
		case <-time.Tick(time.Second * 5):
		case <-s.ctx.Done():
			return
		}
	}
}

//Close 关闭
func (s *StreamLog) Close() error {
	s.cancel()
	s.writer.Close()
	close(s.cacheQueue)
	fmt.Printf("send :%d \n", s.size)
	return nil
}

//Name 返回logger name
func (s *StreamLog) Name() string {
	return name
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
					clusterAddress = append(clusterAddress, fmt.Sprintf("http://%s:%d/docker-instance?service_id=%s&mode=stream", ins.HostIP, ins.WebPort, serviceID))
				}
			}
		}
		if len(clusterAddress) < 1 {
			clusterAddress = append(clusterAddress, defaultClusterAddress+"?service_id="+serviceID+"&mode=stream")
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
