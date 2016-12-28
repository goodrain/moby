package stdutil

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/container"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon"
)

//Watcher 观看者
type Watcher interface {
	Watch()
	WaitStop()
}

//StdWatcher 观察容器标准输出，通过设置判断是否需要将标准输出输入到某容器标准输入
type StdWatcher struct {
	RunDaemon   *daemon.Daemon
	Handle      chan *container.Container
	CopyWork    map[string]*WatcherCopy
	CWLock      sync.Mutex
	Close, stop chan struct{}
	WatchImage  string
}

//CreateWatcher 创建容器标准输入输出观察者
func CreateWatcher(d *daemon.Daemon, close chan struct{}, watchimage string) Watcher {
	logrus.Info("RunContaiers StdWatch has completed create")
	w := &StdWatcher{
		RunDaemon:  d,
		Handle:     make(chan *container.Container, 5),
		CopyWork:   make(map[string]*WatcherCopy, 0),
		Close:      close,
		stop:       make(chan struct{}),
		WatchImage: watchimage,
	}
	return w
}

func (sw *StdWatcher) clear() {
	sw.CWLock.Lock()
	defer sw.CWLock.Unlock()
	for _, v := range sw.CopyWork {
		close(v.closed)
	}
	sw.CopyWork = nil
}

func (sw *StdWatcher) handle() {
	for {
		select {
		case <-sw.Close:
			logrus.Debug("StdWatcher handle closed")
			return
		case c := <-sw.Handle:
			sw.CWLock.Lock()
			if _, ok := sw.CopyWork[c.ID]; !ok {
				var serviceID, servicePod, tenantID string
				for _, env := range c.Config.Env {
					if strings.HasPrefix(env, "SERVICE_ID") {
						serviceID = strings.Split(env, "=")[1]
					}
					if strings.HasPrefix(env, "SERVICE_POD") {
						servicePod = strings.Split(env, "=")[1]
					}
					if strings.HasPrefix(env, "TENANT_ID") {
						tenantID = strings.Split(env, "=")[1]
					}
				}
				if serviceID == "" || servicePod == "" || tenantID == "" {
					logrus.Warningf("The WatchContainer (%s) is not define endpoint", c.Name)
				} else {
					logrus.Debugf("Watch a contaier that want to read stdout from contaienr name: k8s_%s.*_%s_%s_*", serviceID, servicePod, tenantID)
					watchcopy := NewWatcherCopy(c, sw.RunDaemon)
					if watchcopy != nil {
						var findfunc = func(c *container.Container) bool {
							return strings.HasPrefix(c.Name, "/k8s_"+serviceID) && strings.Contains(c.Name, servicePod+"_"+tenantID)
						}
						go watchcopy.find(findfunc)
						sw.CopyWork[c.ID] = watchcopy
					}
				}
			}
			sw.CWLock.Unlock()
		}
	}
}

//checkheath
// 健康检测，清除超时未找到数据源的watchercopy
// 清除关闭的容器,查看daemon怎么维护已关闭和已删除容器
// 转发状态监测，若出错尝试重新链接
func (sw *StdWatcher) checkheath() {
	for {
		select {
		case <-sw.Close:
			logrus.Debug("stdwatcher checkheath close")
			return
		case <-time.Tick(time.Minute * 1):
		}
		sw.CWLock.Lock()
		var cacheDelete []string
		for k, v := range sw.CopyWork {
			if !v.isFound && v.createTime.Add(time.Minute*5).Before(time.Now()) {
				cacheDelete = append(cacheDelete, k)
			}
			if (v.srcContainer != nil && !v.srcContainer.IsRunning()) || (v.dstContainer != nil && !v.dstContainer.IsRunning()) {
				cacheDelete = append(cacheDelete, k)
			}
			if v.status == "error" { //发生错误，尝试重连
				v.Run()
			}
		}
		if len(cacheDelete) > 0 {
			for _, id := range cacheDelete {
				if work, ok := sw.CopyWork[id]; ok {
					logrus.Debug("clear the std copyer ", id)
					close(work.closed)
					delete(sw.CopyWork, id)
				}
			}
		}
		sw.CWLock.Unlock()
	}
}

//Watch 容器标准输入输出观察启动
func (sw *StdWatcher) Watch() {
	defer func() {
		if err := recover(); err != nil {
			logrus.Error("StdWatcher happen pinic")
		}
	}()

	go sw.handle()
	go sw.checkheath()

	logrus.Info("Run Contaiers StdWatch Start")
	logrus.Debug("WatchStdIn container that image is ", sw.WatchImage)
	for {
		for _, c := range sw.RunDaemon.List() {
			if c.IsRunning() && (c.ImageID.String() == sw.WatchImage || c.Config.Image == sw.WatchImage) {
				sw.Handle <- c
			}
		}
		select {
		case <-sw.Close:
			close(sw.Handle)
			sw.clear()
			logrus.Debug("stdwatcher watch close")
			close(sw.stop)
			return
		case <-time.Tick(time.Second * 5):
		}
	}
}

//WaitStop 等待关闭完成
func (sw *StdWatcher) WaitStop() {
	<-sw.stop
}

//WatcherCopy 转发看到的标准输出数据
type WatcherCopy struct {
	srcContainer, dstContainer *container.Container
	srcs                       map[string]io.Reader
	dst                        io.WriteCloser
	copyJobs                   sync.WaitGroup
	closed                     chan struct{}
	isFound                    bool
	daemon                     *daemon.Daemon
	createTime                 time.Time
	status                     string
}

// NewWatcherCopy creates a new Copier
func NewWatcherCopy(container *container.Container, d *daemon.Daemon) *WatcherCopy {
	if container == nil {
		return nil
	}
	return &WatcherCopy{
		dstContainer: container,
		dst:          container.StdinPipe(),
		closed:       make(chan struct{}),
		daemon:       d,
		createTime:   time.Now(),
	}
}

//Run 开始
func (c *WatcherCopy) Run() {
	for src, w := range c.srcs {
		c.copyJobs.Add(1)
		c.status = "running"
		go c.copySrc(src, w)
	}
}

func (c *WatcherCopy) copySrc(name string, src io.Reader) {
	defer c.copyJobs.Done()
	defer func() {
		if err := recover(); err != nil {
			c.status = "error"
			logrus.Error("WatcherCopy copySrc happen pinic")
		}
	}()
	reader := bufio.NewReader(src)
	for {
		select {
		case <-c.closed:
			c.dst.Close()
			c.status = "closed"
			return
		default:
			line, err := reader.ReadBytes('\n')
			//line = bytes.TrimSuffix(line, []byte{'\n'})
			// ReadBytes can return full or partial output even when it failed.
			// e.g. it can return a full entry and EOF.
			if err == nil || len(line) > 0 {
				c.dst.Write(line)
			}
			if err != nil {
				if err != io.EOF {
					logrus.Errorf("Error scanning log stream: %s", err)
				}
				c.status = "error"
				return
			}
		}
	}
}

func (c *WatcherCopy) find(f func(*container.Container) bool) {
	if !c.isFound && c.srcs == nil {
		for {
			for _, container := range c.daemon.List() {
				if container.IsRunning() && f(container) {
					c.srcs = map[string]io.Reader{"stdout": container.StdoutPipe(), "stderr": container.StderrPipe()}
					c.srcContainer = container
					logrus.Debug("WatcherCopy Find the source container ", container.ID)
					c.isFound = true
					go c.Run()
					return
				}
			}
			select {
			case <-time.Tick(time.Second * 5):
			}
		}
	}
}
