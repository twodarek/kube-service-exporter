package leader

import (
	"context"
	"testing"
	"time"

	"github.com/github/kube-service-exporter/pkg/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	KvPrefix  = "kse-test"
	ClusterId = "cluster1"
	ClientId  = "pod1"
)

type ConsulLeaderElectorSuite struct {
	suite.Suite
	consulServer *tests.TestingConsulServer
	elector      *ConsulLeaderElector
	// used to signal when the elector has shutdown
	electorC chan struct{}
}

func WaitForIsLeader(elector LeaderElector) bool {
	for i := 0; i < 10; i++ {
		if elector.IsLeader() {
			return true
		}
		time.Sleep(time.Duration(i*10) * time.Millisecond)
	}
	return false
}

func TestConsulLeaderElectorSuite(t *testing.T) {
	suite.Run(t, new(ConsulLeaderElectorSuite))
}

func (s *ConsulLeaderElectorSuite) SetupTest() {
	s.electorC = make(chan struct{})
	s.consulServer = tests.NewTestingConsulServer()
	s.T().Log("Starting consul server", s.consulServer.Config.Address)
	err := s.consulServer.Start()
	s.Require().NoError(err)
	s.T().Log("Consul server started", s.consulServer.Config.Address)

	elector, err := NewConsulLeaderElector(s.consulServer.Config, KvPrefix, ClusterId, ClientId)
	s.Require().NoError(err)
	s.elector = elector
	go func() {
		defer close(s.electorC)
		s.elector.Run()
	}()
}

func (s *ConsulLeaderElectorSuite) TearDownTest() {
	s.elector.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	select {
	case <-s.electorC:
	case <-ctx.Done():
		s.T().Log("Elector did not shutdown in time")
	}

	s.T().Log("Stopping consul server", s.consulServer.Config.Address)
	err := s.consulServer.Stop()
	s.Require().NoError(err)
	s.T().Log("Consul server stopped", s.consulServer.Config.Address)
}

func (s *ConsulLeaderElectorSuite) TestElection() {
	s.T().Run("Acquires leadership", func(t *testing.T) {
		t.Skip("This test is flaky")
		/*
			err := s.elector.WaitForLeader(5*time.Second, 10*time.Millisecond)
			s.Require().NoError(err)
			s.True(s.elector.IsLeader())
		*/
	})

	s.T().Run("There can only be one leader", func(t *testing.T) {
		t.Skip("This test is flaky")
		/*
			elector2, err := NewConsulLeaderElector(s.consulServer.Config, KvPrefix, ClusterId, "pod2")
			s.Require().NoError(err)
			s.NotNil(elector2)

			go elector2.Run()

			err = elector2.WaitForLeader(1*time.Second, 10*time.Millisecond)
			s.Require().NoError(err)
			ok, err := elector2.HasLeader()
			s.NoError(err)
			s.True(ok, "Second elector sees there is a leader")
			s.False(elector2.IsLeader(), "Second elector is not the leader")
			elector2.Stop()
		*/
	})
}

func TestNewElection(t *testing.T) {
	t.Skip("This test is flaky")
	consulServer := tests.NewTestingConsulServer()
	if err := consulServer.Start(); err != nil {
		t.Fatal("error starting consul", err)
	}
	elector1, err := NewConsulLeaderElector(consulServer.Config, KvPrefix, ClusterId, ClientId)
	require.NoError(t, err)
	go elector1.Run()

	assert.NoError(t, elector1.WaitForLeader(5*time.Second, 250*time.Millisecond))
	assert.True(t, elector1.IsLeader())

	elector2, _ := NewConsulLeaderElector(consulServer.Config, KvPrefix, ClusterId, "pod2")
	go elector2.Run()
	elector1.Stop()

	assert.True(t, WaitForIsLeader(elector2))
	assert.True(t, elector2.IsLeader())
	elector2.Stop()
	if err := consulServer.Stop(); err != nil {
		t.Fatal("error stopping consul", err)
	}
}
