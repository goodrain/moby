package zmqlog

import (
	"fmt"
	//"sync"
	"strings"

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
	containerId := ctx.ContainerID[:12]
	zmqaddress := ctx.Config[zmqAddress]
	fmt.Println("zmqaddress: ", zmqaddress)
	
	puber, err := zmq.NewSocket(zmq.PUB)
	if err != nil {
		return nil, err
	}
	var (
		env = make(map[string]string)
		tenantId string
		serviceId string
	)
	for _, pair := range ctx.ContainerEnv {
		p := strings.SplitN(pair, "=", 2)
		//logrus.Errorf("ContainerEnv pair: %s", pair)
		if len(p) == 2 {
			key :=p[0]
			value :=p[1]
			env[key] = value
		}
	}
	tenantId = env["TENANT_ID"]
	serviceId = env["SERVICE_ID"]
	
	if tenantId == "" {
		tenantId = "default"
	}

	if serviceId == "" {
		serviceId = "default"
	}

	puber.Connect(zmqaddress)

	return &ZmqLogger{
		writer: puber,
		containerId: containerId,
		tenantId : tenantId,
		serviceId : serviceId,
	}, nil
}

func (s *ZmqLogger) Log(msg *logger.Message) error {
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