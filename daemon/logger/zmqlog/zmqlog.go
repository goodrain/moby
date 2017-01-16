package zmqlog

import (
	"fmt"
	"sync"

	"github.com/Sirupsen/logrus"
	zmq "github.com/pebbe/zmq4"
	"github.com/docker/docker/daemon/logger"
)

const (
	name 				= "zmqlog"
	zmqAddress 			= "zmq-address"
)

type ZmqLogger struct {
	writer *zmq.Socket
	containerId string
	tenantId string
	serviceId string
	lock      sync.Mutex
}

func init() {
	if err := logger.RegisterLogDriver(name, New); err != nil {
		logrus.Fatal(err)
	}
	if err := logger.RegisterLogOptValidator(name, ValidateLogOpt); err != nil {
		logrus.Fatal(err)
	}
}

func New(ctx logger.Context) (logger.Logger, error) {
	zmqaddress := ctx.Config[zmqAddress]
	
	puber, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		return nil, err
	}
	var (
		tenantId string
		serviceId string
	)

	attrs := ctx.ExtraAttributes(nil)
	tenantId = attrs["TENANT_ID"]
	serviceId = attrs["SERVICE_ID"]
	
	if tenantId == "" {
		tenantId = "default"
	}

	if serviceId == "" {
		serviceId = "default"
	}

	puber.Connect(zmqaddress)

	return &ZmqLogger{
		writer: puber,
		containerId: ctx.ID(),
		tenantId : tenantId,
		serviceId : serviceId,
	}, nil
}

func (s *ZmqLogger) Log(msg *logger.Message) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.writer.Send(s.tenantId, zmq.SNDMORE)
	s.writer.Send(s.serviceId, zmq.SNDMORE)
	if msg.Source == "stderr" {
		s.writer.Send(s.containerId + ": " + string(msg.Line), zmq.DONTWAIT)
	} else {
		s.writer.Send(s.containerId + ": " + string(msg.Line), zmq.DONTWAIT)
	}
	return nil
}

func (s *ZmqLogger) Close() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.writer != nil {
		return s.writer.Close()
	}
	return nil
}

func (s *ZmqLogger) Name() string {
	return name
}

func ValidateLogOpt(cfg map[string]string) error {
	for key := range cfg {
		switch key {
		case zmqAddress:
		default:
			return fmt.Errorf("unknown log opt '%s' for %s log driver", key, name)
		}
	}
	if cfg[zmqAddress] == "" {
		return fmt.Errorf("must specify a value for log opt '%s'", zmqAddress)
	}
	return nil
}