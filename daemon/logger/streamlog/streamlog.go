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
	"github.com/barnettzqg/buffstreams"
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
	writer        *buffstreams.TCPConn
	serviceID     string
	tenantID      string
	containerID   string
	errorQueue    [][]byte
	reConnecting  chan bool
	serverAddress string
	ctx           context.Context
	cancel        context.CancelFunc
	cacheSize     int
	cacheQueue    chan []byte
	lock          sync.Mutex
	config        map[string]string
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
	cfg := getTCPConnConfig(serviceID, ctx.Config["stream-server"])
	writer, err := buffstreams.DialTCP(cfg)
	if err != nil {
		return nil, err
	}
	cacheSize, err := strconv.Atoi(ctx.Config["cache-error-log-size"])
	if err != nil {
		cacheSize = 2048
	}
	currentCtx, cancel := context.WithCancel(context.Background())
	logger := &StreamLog{
		writer:       writer,
		serviceID:    serviceID,
		tenantID:     tenantID,
		containerID:  ctx.ContainerID,
		ctx:          currentCtx,
		cancel:       cancel,
		cacheSize:    cacheSize,
		config:       ctx.Config,
		reConnecting: make(chan bool, 1),
		cacheQueue:   make(chan []byte),
	}
	go logger.send()
	return logger, nil
}

func getTCPConnConfig(serviceID, address string) *buffstreams.TCPConnConfig {
	if address == "" {
		address = GetLogAddress(serviceID)
	}
	if strings.HasPrefix(address, "tcp://") {
		address = address[6:]
	}
	cfg := &buffstreams.TCPConnConfig{
		MaxMessageSize: 4096,
		Address:        address,
	}
	return cfg
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

func (s *StreamLog) errorLog(msg []byte) {
	if len(s.errorQueue) < 10 {
		s.errorQueue = append(s.errorQueue, msg)
	} else {
		s.sendCache()
		s.errorQueue = append(s.errorQueue, msg)
	}
}
func (s *StreamLog) sendCache() {
	for i := 0; i < len(s.errorQueue); i++ {
		msg := s.errorQueue[i]
		if len(s.cacheQueue) < s.cacheSize {
			s.cacheQueue <- msg
		}
	}
	s.errorQueue = s.errorQueue[:0]
}

func (s *StreamLog) send() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg := <-s.cacheQueue:
			if s.writer != nil {
				time.Sleep(1 * time.Millisecond)
				_, err := s.writer.Write(msg)
				if err != nil {
					logrus.Debug("send log message to stream server error.", err.Error())
					s.errorLog(msg)
					if isConnectionClosed(err.Error()) && len(s.reConnecting) < 1 {
						go s.reConect()
					}
				}
			} else {
				if len(s.reConnecting) < 1 {
					go s.reConect()
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
	defer buf.Reset()
	buf.WriteString(`{"container_id":"`)
	buf.WriteString(s.containerID[0:12])
	buf.WriteString(`","msg":`)
	ffjsonWriteJSONBytesAsString(buf, msg.Line)
	buf.WriteString(`,"time":"`)
	buf.WriteString(msg.Timestamp.Format(time.RFC3339))
	buf.WriteString(`","service_id":"`)
	buf.WriteString(s.serviceID)
	buf.WriteString(`"}`)
	stream := buf.Bytes()
	if len(stream) > 4096 {
		logrus.Warnf("log length too long (%s)", string(stream))
		return nil
	}
	if len(s.cacheQueue) < s.cacheSize {
		s.cacheQueue <- stream
	} else {
		s.errorLog(stream)
	}
	return nil
}

func isConnectionClosed(err string) bool {
	return strings.HasSuffix(err, "connection refused") || strings.HasSuffix(err, "use of closed network connection")
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
				go s.sendCache()
				return
			}
		}
		//step2 get new server address and reconnect
		cfg := getTCPConnConfig(s.serviceID, s.config["stream-server"])
		if cfg.Address == s.serverAddress {
			logrus.Warning("stream log server address not change ,will reconnect")
			err := s.writer.ReConnect()
			if err != nil {
				logrus.Error("stream log server connect error." + err.Error())
			} else {
				go s.sendCache()
				return
			}
		} else {
			writer, err := buffstreams.DialTCP(cfg)
			if err != nil {
				logrus.Errorf("stream log server connect %s error. %v", cfg.Address, err.Error())
			} else {
				s.writer = writer
				go s.sendCache()
				return
			}
		}

		select {
		case <-time.Tick(time.Second * 2):
		case <-s.ctx.Done():
			return
		}
	}
}

//Close 关闭
func (s *StreamLog) Close() error {
	s.cancel()
	if len(s.errorQueue) > 0 {
		s.sendCache()
	}
	s.writer.Close()
	s.writer = nil
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
