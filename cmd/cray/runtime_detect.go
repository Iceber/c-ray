package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	containerdclient "github.com/containerd/containerd/v2/client"
	dockerclient "github.com/docker/docker/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const runtimeProbeTimeout = 1500 * time.Millisecond

var (
	crioSocketCandidates = []string{
		"/run/crio/crio.sock",
		"/var/run/crio/crio.sock",
	}
	containerdSocketCandidates = []string{
		"/run/containerd/containerd.sock",
		"/var/run/containerd/containerd.sock",
	}
	dockerSocketCandidates = []string{
		"/var/run/docker.sock",
		"/run/docker.sock",
	}
)

type runtimeDetector struct {
	isSocket           func(string) bool
	supportsCRI        func(string) bool
	supportsDocker     func(string) bool
	supportsContainerd func(string) bool
}

func defaultRuntimeDetector() runtimeDetector {
	return runtimeDetector{
		isSocket:           isUnixSocket,
		supportsCRI:        probeCRISocket,
		supportsDocker:     probeDockerSocket,
		supportsContainerd: probeContainerdSocket,
	}
}

func detectRuntime(sock string) (string, string, error) {
	return detectRuntimeWith(defaultRuntimeDetector(), sock)
}

func detectRuntimeWith(detector runtimeDetector, sock string) (string, string, error) {
	if sock != "" {
		return detector.detectExplicit(sock)
	}
	return detector.detectAuto()
}

func (d runtimeDetector) detectExplicit(sock string) (string, string, error) {
	if d.isSocket != nil && d.isSocket(sock) {
		if d.supportsCRI != nil && d.supportsCRI(sock) {
			if looksLikeCRIOSocket(sock) {
				return "crio", sock, nil
			}
			if d.supportsContainerd != nil && d.supportsContainerd(sock) {
				return "containerd", sock, nil
			}
			return "crio", sock, nil
		}
		if d.supportsDocker != nil && d.supportsDocker(sock) {
			return "docker", sock, nil
		}
		if d.supportsContainerd != nil && d.supportsContainerd(sock) {
			return "containerd", sock, nil
		}
	}

	return inferRuntimeFromPath(sock), sock, nil
}

func (d runtimeDetector) detectAuto() (string, string, error) {
	for _, sock := range crioSocketCandidates {
		if !d.isSocket(sock) {
			continue
		}
		if d.supportsCRI != nil && d.supportsCRI(sock) {
			return "crio", sock, nil
		}
	}

	for _, sock := range containerdSocketCandidates {
		if !d.isSocket(sock) {
			continue
		}
		if d.supportsContainerd != nil && d.supportsContainerd(sock) && d.supportsCRI != nil && d.supportsCRI(sock) {
			return "containerd", sock, nil
		}
	}

	for _, sock := range dockerSocketCandidates {
		if !d.isSocket(sock) {
			continue
		}
		if d.supportsDocker != nil && d.supportsDocker(sock) {
			return "docker", sock, nil
		}
	}

	for _, sock := range containerdSocketCandidates {
		if !d.isSocket(sock) {
			continue
		}
		if d.supportsContainerd != nil && d.supportsContainerd(sock) {
			return "containerd", sock, nil
		}
	}

	return "", "", fmt.Errorf("runtime socket auto-detection failed: checked CRI-O, CRI-enabled containerd, Docker, and containerd sockets; use -socket or CRAY_SOCKET to specify one explicitly")
}

func inferRuntimeFromPath(sock string) string {
	switch {
	case looksLikeCRIOSocket(sock):
		return "crio"
	case strings.Contains(sock, "docker"):
		return "docker"
	case strings.Contains(sock, "containerd"):
		return "containerd"
	default:
		return "containerd"
	}
}

func looksLikeCRIOSocket(sock string) bool {
	return strings.Contains(sock, "crio")
}

func isUnixSocket(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Type() == os.ModeSocket
}

func probeCRISocket(socketPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeProbeTimeout)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}),
		grpc.WithBlock(),
	)
	if err != nil {
		return false
	}
	defer conn.Close()

	_, err = runtimeapi.NewRuntimeServiceClient(conn).Status(ctx, &runtimeapi.StatusRequest{})
	return err == nil
}

func probeDockerSocket(socketPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeProbeTimeout)
	defer cancel()

	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithAPIVersionNegotiation(),
		dockerclient.WithHost("unix://"+socketPath),
	)
	if err != nil {
		return false
	}
	defer cli.Close()

	_, err = cli.Ping(ctx)
	return err == nil
}

func probeContainerdSocket(socketPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeProbeTimeout)
	defer cancel()

	client, err := containerdclient.New(socketPath)
	if err != nil {
		return false
	}
	defer client.Close()

	_, err = client.IntrospectionService().Server(ctx)
	return err == nil
}
