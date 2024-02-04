package docker

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"io"
	"log/slog"
	"os"
	"strings"
)

type ContainerCreateBuilder struct {
	containerConfig  *container.Config
	hostConfig       *container.HostConfig
	networkingConfig *network.NetworkingConfig
	platform         *v1.Platform
	containerName    string
	err              error
	ctx              context.Context
}

func (self *ContainerCreateBuilder) WithContainerName(name string) *ContainerCreateBuilder {
	self.containerConfig.Hostname = fmt.Sprintf("%s.pod.dpanel.local", name)
	self.containerName = name
	self.containerConfig.Labels = map[string]string{
		"BuildAuthor":  BuilderAuthor,
		"BuildDesc":    BuildDesc,
		"BuildWebSite": BuildWebSite,
		"buildVersion": BuildVersion,
	}
	//  防止退出
	self.containerConfig.AttachStdin = true
	self.containerConfig.AttachStdout = true
	self.containerConfig.AttachStderr = true
	self.containerConfig.Tty = true
	return self
}

func (self *ContainerCreateBuilder) WithEnv(name string, value string) *ContainerCreateBuilder {
	self.containerConfig.Env = append(self.containerConfig.Env, fmt.Sprintf("%s=%s", name, value))
	return self
}

func (self *ContainerCreateBuilder) WithImage(image string, tryPullImage bool) {
	// 只尝试从 docker.io 拉取
	if tryPullImage {
		reader, err := Sdk.Client.ImagePull(Sdk.Ctx, image, types.ImagePullOptions{})
		if err != nil {
			self.err = err
			return
		}
		io.Copy(os.Stdout, reader)
	}
	self.containerConfig.Image = image
	return
}

func (self *ContainerCreateBuilder) WithRestart(restartType string) *ContainerCreateBuilder {
	self.hostConfig.RestartPolicy = container.RestartPolicy{}
	self.hostConfig.RestartPolicy.Name = Sdk.GetRestartPolicyByString(restartType)
	return self
}

func (self *ContainerCreateBuilder) WithPrivileged() *ContainerCreateBuilder {
	self.hostConfig.Privileged = true
	return self
}

// WithVolume 挂载宿主机目录
func (self *ContainerCreateBuilder) WithVolume(host string, container string, permission string) *ContainerCreateBuilder {
	//_, err := os.Stat(host)
	self.hostConfig.Binds = append(self.hostConfig.Binds, fmt.Sprintf("%s:%s:%s", host, container, permission))
	return self
}

// WithContainerVolume 挂载某个容器的数据卷
func (self *ContainerCreateBuilder) WithContainerVolume(fromContainerMd5 string) {
	self.hostConfig.VolumesFrom = append(self.hostConfig.VolumesFrom, fromContainerMd5)
}

func (self *ContainerCreateBuilder) WithDefaultVolume(container string) {
	volumePath := fmt.Sprintf("%s.%s", self.containerName, strings.Join(strings.Split(container, "/"), "-"))
	self.hostConfig.Binds = append(self.hostConfig.Binds, fmt.Sprintf("%s:%s:rw", volumePath, container))
}

// WithPort 绑定端口
func (self *ContainerCreateBuilder) WithPort(host string, container string) *ContainerCreateBuilder {
	var port nat.Port
	var err error
	hostIp := "0.0.0.0"
	if strings.Contains(container, "/") {
		portArr := strings.Split(container, "/")
		port, err = nat.NewPort(portArr[1], portArr[0])
	} else {
		port, err = nat.NewPort("tcp", container)
	}
	if err != nil {
		self.err = err
		return nil
	}
	self.containerConfig.ExposedPorts[port] = struct{}{}
	self.hostConfig.PortBindings[port] = make([]nat.PortBinding, 0, 1)
	self.hostConfig.PortBindings[port] = append(
		self.hostConfig.PortBindings[port], nat.PortBinding{HostIP: hostIp, HostPort: host},
	)
	return self
}

func (self *ContainerCreateBuilder) WithLink(name string, alise string) {
	// 利用Network关联容器
	options := make(map[string]string)
	options["name"] = self.containerName
	myNetwork, _ := Sdk.Client.NetworkCreate(Sdk.Ctx, self.containerName, types.NetworkCreate{
		CheckDuplicate: true,
		Driver:         "bridge",
		Options:        options,
	})
	slog.Debug("create network", "name", myNetwork.ID)
	// 关联网络时，重新退出加入
	err := Sdk.Client.NetworkDisconnect(Sdk.Ctx, self.containerName, name, true)
	if err != nil {
		slog.Debug("disconnect network", "name", myNetwork.ID, "error", err.Error())
	}
	err = Sdk.Client.NetworkConnect(Sdk.Ctx, self.containerName, name, &network.EndpointSettings{
		Aliases: []string{
			alise,
		},
	})
	if err != nil {
		slog.Debug("join network", "name", myNetwork.ID, "error", err.Error())
	}
}

func (self *ContainerCreateBuilder) WithAutoRemove() {
	self.hostConfig.AutoRemove = true
}

func (self *ContainerCreateBuilder) WithCpus(count int) {
	self.hostConfig.NanoCPUs = int64(count) * 1000000000
}

func (self *ContainerCreateBuilder) WithMemory(count int) {
	self.hostConfig.Memory = int64(count) * 1024 * 1024
}

func (self *ContainerCreateBuilder) WithShmSize(size int) {
	self.hostConfig.ShmSize = int64(size)
}

func (self *ContainerCreateBuilder) WithPid(pid ...string) {
	pidStr := strings.Join(pid, ":")
	self.hostConfig.PidMode = container.PidMode(pidStr)
}

func (self *ContainerCreateBuilder) Execute() (response container.CreateResponse, err error) {
	if self.err != nil {
		return response, self.err
	}
	return Sdk.Client.ContainerCreate(
		self.ctx,
		self.containerConfig,
		self.hostConfig,
		self.networkingConfig,
		self.platform,
		self.containerName,
	)
}
