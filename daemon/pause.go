package daemon

import (
	"fmt"
	"syscall"
	
	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/container"
)

// ContainerPause pauses a container
func (daemon *Daemon) ContainerPause(name string) error {
	container, err := daemon.GetContainer(name)
	if err != nil {
		return err
	}

	if err := daemon.containerPause(container); err != nil {
		return err
	}

	return nil
}

// containerPause pauses the container execution without stopping the process.
// The execution can be resumed by calling containerUnpause.
func (daemon *Daemon) containerPause(container *container.Container) error {

	// We cannot Pause the container which is not running
	if !container.Running {
		return errNotRunning{container.ID}
	}

	// We cannot Pause the container which is already paused
	if container.Paused {
		return fmt.Errorf("Container %s is already paused", container.ID)
	}

	// We cannot Pause the container which is restarting
	if container.Restarting {
		return errContainerIsRestarting(container.ID)
	}
	
	daemon.stopHealthchecks(container)
	
	if err := daemon.containerd.Pause(container.ID); err != nil {
		return fmt.Errorf("Cannot pause container %s: %s", container.ID, err)
	}
	
	// 1. Send a stop signal
	if err := daemon.kill(container, int(syscall.SIGKILL)); err != nil {
		logrus.Infof("Failed to kill the process, force killing")
		if err := daemon.kill(container, int(syscall.SIGKILL)); err != nil {
			return err
		}
	}
	
	return nil
}
