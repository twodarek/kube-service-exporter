package tests

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	capi "github.com/hashicorp/consul/api"
)

const (
	ConsulHTTPPortBase = 28500 // some port that's not Consul's production port
)

type TestingConsulServer struct {
	cmd      *exec.Cmd
	NodeName string
	Client   *capi.Client
	Config   *capi.Config
}

func NewTestingConsulServer() *TestingConsulServer {
	s1 := rand.NewSource(time.Now().UnixNano())
	r1 := rand.New(s1)
	consulHttpPort := strconv.Itoa(ConsulHTTPPortBase + r1.Intn(99))

	config := capi.DefaultConfig()
	config.Address = fmt.Sprintf("127.0.0.1:%s", consulHttpPort)
	nodeName := fmt.Sprintf("consul-test-server-%s", consulHttpPort)
	cmd := exec.Command("consul",
		"agent",
		"-dev",
		"-server-port", strconv.Itoa(28400+r1.Intn(99)),
		"-http-port", consulHttpPort,
		"-grpc-port=0",
		"-grpc-tls-port=0",
		"-dns-port=0",
		"-serf-lan-port", strconv.Itoa(28300+r1.Intn(99)),
		"-serf-wan-port", strconv.Itoa(28200+r1.Intn(99)),
		"-bind=127.0.0.1",
		"-node", nodeName)
	cmd.WaitDelay = 1 * time.Second

	return &TestingConsulServer{
		cmd:      cmd,
		NodeName: nodeName,
		Config:   config}
}

// Start consul in dev mode
// Logs will go to stdout/stderr
// Each outer Test* func will get a freshly restarted consul
func (server *TestingConsulServer) Start() error {
	if err := server.cmd.Start(); err != nil {
		return err
	}

	client, err := capi.NewClient(server.Config)
	if err != nil {
		return err
	}
	server.Client = client

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// keep trying to write to consul until it starts
	ticker := time.NewTicker(10 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			return errors.New("took too long to start consul")
		case <-ticker.C:
			_, err := client.KV().Put(&capi.KVPair{Key: "test", Value: []byte("bar")}, nil)
			if err == nil {
				return nil
			}
		}
	}
}

// Stop consul.  Wait up to 2 seconds before killing it forcefully
func (server *TestingConsulServer) Stop() error {
	server.cmd.Process.Signal(syscall.SIGTERM)
	stoppedC := make(chan struct{})
	go func() {
		defer close(stoppedC)
		server.cmd.Wait()
	}()

	// make sure the stop doesn't take to long
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-timer.C:
		server.cmd.Process.Kill()
		return errors.New("took too long to stop consul")
	case <-stoppedC:
	}

	return nil
}
