package controller

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/github/kube-service-exporter/pkg/queue"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func init() {
	log.SetOutput(io.Discard) // Disable logging
}

func chanRecvWithTimeout(t *testing.T, c chan string) string {
	t.Helper()
	select {
	case val := <-c:
		return val
	// this value needs to be longer than the wait.Until() interval in Run()
	case <-time.After(15 * time.Second):
		t.Error("Timed out")
	}
	return ""
}

// A fake, in memory, transparent ExportTarget for testing
// Each of the C[r]UD methods sends a blocking event to EventC so the caller
// can detect that the method was called.
type fakeTarget struct {
	Store  []*ExportedService
	Nodes  map[string]ExportedNode
	EventC chan string
	mutex  sync.RWMutex
}

func NewFakeTarget() *fakeTarget {
	return &fakeTarget{
		Store:  make([]*ExportedService, 0),
		EventC: make(chan string)}
}

func (t *fakeTarget) Create(es *ExportedService) (bool, error) {
	if _, found := t.find(es); found {
		return false, nil
	}

	t.mutex.Lock()
	t.Store = append(t.Store, es)
	t.mutex.Unlock()
	t.EventC <- "create"
	return true, nil
}

func (t *fakeTarget) Update(old *ExportedService, es *ExportedService) (bool, error) {
	if idx, ok := t.find(es); ok {
		t.mutex.Lock()
		t.Store[idx] = es
		t.mutex.Unlock()
		t.EventC <- "update"
		return true, nil
	}
	return t.Create(es)
}

func (t *fakeTarget) Delete(es *ExportedService) (bool, error) {
	if idx, ok := t.find(es); ok {
		t.mutex.Lock()
		t.Store = append(t.Store[:idx], t.Store[idx+1:]...)
		t.mutex.Unlock()
		t.EventC <- "delete"
		return true, nil
	}

	return false, nil
}

func (t *fakeTarget) WriteNodes(nodes []*v1.Node) error {
	exportedNodes := make(map[string]ExportedNode)

	for _, k8sNode := range nodes {
		for _, addr := range k8sNode.Status.Addresses {
			if addr.Type != "InternalIP" {
				continue
			}

			exportedNode := ExportedNode{
				Name:    k8sNode.Name,
				Address: addr.Address,
			}
			exportedNodes[k8sNode.Name] = exportedNode
		}
	}

	t.mutex.Lock()
	t.Nodes = exportedNodes
	t.mutex.Unlock()

	t.EventC <- "write_nodes"
	return nil
}

// GetNodes prevents race conditions when evaluating t.Nodes in tests
func (t *fakeTarget) GetNodes() map[string]ExportedNode {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	return t.Nodes
}

func (t *fakeTarget) find(es *ExportedService) (int, bool) {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	for i, val := range t.Store {
		if val.Id() == es.Id() {
			return i, true
		}
	}
	return 0, false
}

type ServiceWatcherSuite struct {
	suite.Suite
	sw             *ServiceWatcher
	serviceFixture *v1.Service
	target         *fakeTarget
	ic             *InformerConfig
}

func (s *ServiceWatcherSuite) SetupTest() {
	var err error
	ns := &v1.Namespace{ObjectMeta: meta_v1.ObjectMeta{Name: "default"}}

	// set up a fake ListerWatcher and ClientSet
	s.ic = &InformerConfig{
		ClientSet:    fake.NewSimpleClientset(ns),
		ResyncPeriod: time.Duration(0),
	}

	require.NoError(s.T(), err)

	s.ic.ClientSet.CoreV1().Namespaces().Create(context.TODO(), ns, meta_v1.CreateOptions{})
	s.target = NewFakeTarget()

	s.sw = NewServiceWatcher(s.ic, []string{ns.Name}, "cluster", s.target, queue.NewNoopMetrics())

	// An example of a "good" service
	s.serviceFixture = &v1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "service1",
			Namespace: "default",
			Annotations: map[string]string{
				ServiceAnnotationExported: "true",
			},
		},
		Spec: v1.ServiceSpec{
			Type: "LoadBalancer",
			Ports: []v1.ServicePort{
				{
					Name:     "http",
					NodePort: 32123},
				{
					Name:     "thing",
					NodePort: 32124},
			},
		},
	}

	go s.sw.Run()
}

func (s *ServiceWatcherSuite) TearDownTest() {
	s.sw.Stop()
}

// Helper functions to add/modify/delete a service to the k8s store and wait
// until it has gone thru the fake export target via the ListerWatcher
// Modify sometimes will trigger a delete, so what to expect can be configured
// by passing in "delete" or "update" to expect
func (s *ServiceWatcherSuite) SourceExec(f func(runtime.Object), service *v1.Service, expects []string) {
	f(service)
	for _, expect := range expects {
		for i := 0; i < len(service.Spec.Ports); i++ {
			val := chanRecvWithTimeout(s.T(), s.target.EventC)
			s.Equal(expect, val)
		}
	}
}

func (s *ServiceWatcherSuite) TestAdd() {
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Create(context.TODO(), obj.(*v1.Service), meta_v1.CreateOptions{})
		},
		s.serviceFixture,
		[]string{"create"})
	s.Len(s.target.Store, 2)
}

func (s *ServiceWatcherSuite) TestUpdate() {
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Create(context.TODO(), obj.(*v1.Service), meta_v1.CreateOptions{})
		},
		s.serviceFixture,
		[]string{"create"})
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Update(context.TODO(), obj.(*v1.Service), meta_v1.UpdateOptions{})
		},
		s.serviceFixture,
		[]string{"update"})
	s.Len(s.target.Store, 2)
}

func (s *ServiceWatcherSuite) TestDelete() {
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Create(context.TODO(), obj.(*v1.Service), meta_v1.CreateOptions{})
		},
		s.serviceFixture,
		[]string{"create"})
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Delete(context.TODO(), obj.(*v1.Service).Name, meta_v1.DeleteOptions{})
		},
		s.serviceFixture,
		[]string{"delete"})
	s.Len(s.target.Store, 0)
}

func (s *ServiceWatcherSuite) TestUpdateTriggersDelete() {
	svc := *s.serviceFixture
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Create(context.TODO(), obj.(*v1.Service), meta_v1.CreateOptions{})
		},
		&svc,
		[]string{"create"})
	s.Len(s.target.Store, 2)
	svc.Spec.Type = v1.ServiceTypeClusterIP
	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Update(context.TODO(), obj.(*v1.Service), meta_v1.UpdateOptions{})
		},
		&svc,
		[]string{"delete"})
	s.Len(s.target.Store, 0)
}

func (s *ServiceWatcherSuite) TestUpdateIdTriggersReplace() {
	before := make([]string, 0, 2)
	after := make([]string, 0, 2)
	svc := *s.serviceFixture

	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Create(context.TODO(), obj.(*v1.Service), meta_v1.CreateOptions{})
		},
		&svc,
		[]string{"create"})
	s.Len(s.target.Store, 2)
	for _, es := range s.target.Store {
		before = append(before, es.Id())
	}
	s.ElementsMatch(before, []string{"cluster-default-service1-http", "cluster-default-service1-thing"})

	svc.Annotations[ServiceAnnotationLoadBalancerServicePerCluster] = "false"

	s.SourceExec(
		func(obj runtime.Object) {
			s.ic.ClientSet.CoreV1().Services("default").Update(context.TODO(), obj.(*v1.Service), meta_v1.UpdateOptions{})
		},
		&svc,
		[]string{"create", "delete"})

	for _, es := range s.target.Store {
		after = append(after, es.Id())
	}
	s.Len(s.target.Store, 2)
	s.ElementsMatch(after, []string{"default-service1-http", "default-service1-thing"})
}

func TestServiceWatcherSuite(t *testing.T) {
	suite.Run(t, new(ServiceWatcherSuite))
}
