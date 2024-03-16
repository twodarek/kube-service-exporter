package controller

import (
	"context"
	"fmt"
	"github.com/github/kube-service-exporter/pkg/util"
	"io"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
)

func init() {
	// Disable logging
	log.SetOutput(io.Discard)
	klog.SetOutput(io.Discard)
}

type NodeWatcherSuite struct {
	suite.Suite
	nw     *NodeWatcher
	target *fakeTarget
	ic     *NodeInformerConfig
}

func testingNode() v1.Node {
	return v1.Node{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "a.example.net",
			Labels: map[string]string{
				"role": "node",
			},
			Annotations: map[string]string{},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
		},
		Status: v1.NodeStatus{
			Addresses: []v1.NodeAddress{
				{
					Address: "192.168.10.10",
					Type:    v1.NodeInternalIP,
				},
			},
			Conditions: []v1.NodeCondition{
				{
					Type:   v1.NodeReady,
					Status: v1.ConditionTrue,
				},
			},
		},
	}
}

func (s *NodeWatcherSuite) setupTestWith(nodeSelectors []string, minNodes int) {
	var err error

	s.ic = &NodeInformerConfig{
		ClientSet:    fake.NewSimpleClientset(),
		ResyncPeriod: time.Duration(0),
	}

	require.NoError(s.T(), err)

	s.target = NewFakeTarget()
	nc := NodeConfiguration{
		Selectors: nodeSelectors,
		MinCount:  minNodes,
	}
	s.nw = NewNodeWatcher(s.ic, nc, s.target)

	go s.nw.Run()
}

func (s *NodeWatcherSuite) stopNodeWatcher() {
	s.nw.Stop()
}

func (s *NodeWatcherSuite) TestAddNodes() {
	// TODO refactor so that this works with SetupTest/TeardownTest
	//      currently, the way these tests are written, we need up
	//      with an unbalanced number of starts and stops, which causes
	//      tests to hang and timeout.
	s.setupTestWith([]string{"role=node"}, 1)
	defer s.stopNodeWatcher()

	tests := []struct {
		node v1.Node
		add  bool
	}{
		// happy path
		{node: testingNode(), add: true},
		{node: testingNode()},
		{node: testingNode()},
		{node: testingNode()},
	}

	// unscheduleable nodes are not added
	tests[1].node.Spec.Unschedulable = true
	// NotReady nodes are not added
	tests[2].node.Status.Conditions = []v1.NodeCondition{
		{Type: v1.NodeReady, Status: v1.ConditionFalse},
	}
	// Nodes that don't match the selector are not added
	tests[3].node.Labels = map[string]string{"role": "api"}

	for i, test := range tests {
		test.node.Name = fmt.Sprintf("%d.example.net", i)
		_, err := s.ic.ClientSet.CoreV1().Nodes().Create(context.TODO(), &test.node, meta_v1.CreateOptions{})
		require.NoError(s.T(), err)

		if test.add {
			val := chanRecvWithTimeout(s.T(), s.target.EventC)
			s.Equal("write_nodes", val)
			s.Contains(s.target.GetNodes(), test.node.Name)
		} else {
			s.NotContains(s.target.GetNodes(), test.node.Name)
		}
	}
}

// When we're passed an empty string as a selector, we should add all ready nodes
func (s *NodeWatcherSuite) TestEmptySelectorAddNodes() {
	s.setupTestWith([]string{""}, 1)
	defer s.stopNodeWatcher()

	nodes := []v1.Node{
		testingNode(),
		testingNode(),
	}

	for i, node := range nodes {
		node.Name = fmt.Sprintf("%d.example.net", i)
		_, err := s.ic.ClientSet.CoreV1().Nodes().Create(context.TODO(), &node, meta_v1.CreateOptions{})
		require.NoError(s.T(), err)
		val := chanRecvWithTimeout(s.T(), s.target.EventC)
		s.Equal("write_nodes", val)
	}

	s.Contains(s.target.GetNodes(), "0.example.net")
	s.Contains(s.target.GetNodes(), "1.example.net")
}

func (s *NodeWatcherSuite) TestAddNodesMultipleSelectors() {
	scenarios := []struct {
		minNodes    int
		numNodes    int
		numApi      int
		matchFirst  bool
		matchSecond bool
	}{
		{minNodes: 1, numNodes: 2, numApi: 2, matchFirst: true, matchSecond: false},
		{minNodes: 2, numNodes: 1, numApi: 2, matchFirst: false, matchSecond: true},
		// Test that we pick the last selector with nodes, if no selector has minNodes
		{minNodes: 2, numNodes: 1, numApi: 1, matchFirst: false, matchSecond: true},
		{minNodes: 2, numNodes: 1, numApi: 0, matchFirst: true, matchSecond: false},
	}

	type testData struct {
		node v1.Node
		add  bool
	}

	for _, scenario := range scenarios {
		s.Run(fmt.Sprintf("%v", scenario), func() {
			s.setupTestWith([]string{"role=node", "role=api"}, scenario.minNodes)
			defer s.stopNodeWatcher()

			tests := []testData{}
			for i := 0; i < scenario.numNodes; i++ {
				tests = append(tests, testData{node: testingNode(), add: scenario.matchFirst})
			}
			for i := 0; i < scenario.numApi; i++ {
				node := testingNode()
				node.Labels = map[string]string{"role": "api"}
				tests = append(tests, testData{node: node, add: scenario.matchSecond})
			}

			// Add all the nodes and let the watcher do its thing
			for i := range tests {
				// Modify the tests by index because the loop variable would just be a copy of the element
				tests[i].node.Name = fmt.Sprintf("%d.example.net", i)
				_, err := s.ic.ClientSet.CoreV1().Nodes().Create(context.TODO(), &tests[i].node, meta_v1.CreateOptions{})
				require.NoError(s.T(), err)
				val := chanRecvWithTimeout(s.T(), s.target.EventC)
				s.Equal("write_nodes", val)
			}

			// Now test if they were processed properly.
			nodes := s.target.GetNodes()
			for _, test := range tests {
				if test.add {
					s.Contains(nodes, test.node.Name)
				} else {
					s.NotContains(nodes, test.node.Name)
				}
			}
		})
	}
}

func (s *NodeWatcherSuite) TestUpdateNodes() {
	s.Run("within grace period", func() {
		s.setupTestWith([]string{"role=node"}, 1)
		defer s.stopNodeWatcher()

		// node, with a grace period
		node := testingNode()
		node.Annotations[util.NodeAnnotationPretendReadyUntil] = time.Now().Add(time.Second * 2).Format(time.RFC3339)
		_, err := s.ic.ClientSet.CoreV1().Nodes().Create(context.TODO(), &node, meta_v1.CreateOptions{})
		s.Require().NoError(err)
		val := chanRecvWithTimeout(s.T(), s.target.EventC)
		s.Require().Equal("write_nodes", val)
		s.Require().Contains(s.target.GetNodes(), node.Name, "node should be in the list")

		// make the node unready/unschedulable
		node.Spec.Unschedulable = true

		// update the node - node should still be in the list because grace period has not passed
		newNode, err := s.ic.ClientSet.CoreV1().Nodes().Update(context.TODO(), &node, meta_v1.UpdateOptions{})
		s.Require().True(newNode.Spec.Unschedulable)
		s.Require().NoError(err)
		val = chanRecvWithTimeout(s.T(), s.target.EventC)
		s.Require().Equal("write_nodes", val)
		s.Require().Contains(s.target.GetNodes(), node.Name, "node should still be in the list")

		// wait for the grace period to pass
		val = chanRecvWithTimeout(s.T(), s.target.EventC)
		s.Require().Equal("write_nodes", val)

		s.Eventually(func() bool {
			// wait for the node to be removed from the list
			for _, n := range s.target.GetNodes() {
				if n.Name == node.Name {
					return false
				}
			}
			return true
		}, 10*time.Second, 100*time.Millisecond, "node is removed from the list for being unready past the grace period")
	})

	s.Run("grace period in the past", func() {
		s.setupTestWith([]string{"role=node"}, 1)
		defer s.stopNodeWatcher()

		// node, with a grace period
		node := testingNode()
		node.Annotations[util.NodeAnnotationPretendReadyUntil] = time.Now().Add(-time.Second * 2).Format(time.RFC3339)
		_, err := s.ic.ClientSet.CoreV1().Nodes().Create(context.TODO(), &node, meta_v1.CreateOptions{})
		s.Require().NoError(err)
		val := chanRecvWithTimeout(s.T(), s.target.EventC)
		s.Require().Equal("write_nodes", val)
		s.Require().Contains(s.target.GetNodes(), node.Name, "node should be in the list")

		// make the node unready/unschedulable
		node.Spec.Unschedulable = true

		// update the node - node should still be in the list because grace period has not passed
		newNode, err := s.ic.ClientSet.CoreV1().Nodes().Update(context.TODO(), &node, meta_v1.UpdateOptions{})
		s.Require().True(newNode.Spec.Unschedulable)
		s.Require().NoError(err)
		val = chanRecvWithTimeout(s.T(), s.target.EventC)
		s.Require().Equal("write_nodes", val)
		s.Require().NotContains(s.target.GetNodes(), node.Name, "node should not be in the list")
	})

}

/*
// The Delete here doesn't appear to be triggering the Watch, so this is commented
// out for now.
func (s *NodeWatcherSuite) TestDeleteNodes() {
	node := testingNode()
	_, err := s.ic.ClientSet.CoreV1().Nodes().Create(context.TODO(), &node, meta_v1.CreateOptions{})
	require.NoError(s.T(), err)

	val := chanRecvWithTimeout(s.T(), s.target.EventC)
	s.Equal("write_nodes", val)
	s.Contains(s.target.GetNodes(), node.Name)

	err = s.ic.ClientSet.CoreV1().Nodes().Delete(context.TODO(), node.Name, meta_v1.DeleteOptions{})
	require.NoError(s.T(), err)

	val = chanRecvWithTimeout(s.T(), s.target.EventC)
	s.Equal("write_nodes", val)
	s.NotContains(s.target.GetNodes(), node.Name)
	fmt.Println(s.target.GetNodes())
}
*/

func TestNodeWatcherSuite(t *testing.T) {
	suite.Run(t, new(NodeWatcherSuite))
}
