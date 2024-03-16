package controller

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/github/kube-service-exporter/pkg/queue"
	"github.com/github/kube-service-exporter/pkg/stats"
	"github.com/github/kube-service-exporter/pkg/util"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	informers_v1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type NodeConfiguration struct {
	Selectors []string
	MinCount  int
	// MaxRequeueCount is the number of times to retry a node before giving up.
	MaxRequeueCount int
}

type NodeWatcher struct {
	informer        informers_v1.NodeInformer
	stopC           chan struct{}
	wg              sync.WaitGroup
	curSelector     string
	clientset       kubernetes.Interface
	queue           workqueue.RateLimitingInterface
	maxRequeueCount int
	target          ExportTarget
	NodeConfiguration
}

type NodeInformerConfig struct {
	ClientSet    kubernetes.Interface
	ResyncPeriod time.Duration
}

func NewNodeInformerConfig() (*NodeInformerConfig, error) {
	cs, err := NewClientSet()
	if err != nil {
		return nil, err
	}

	return &NodeInformerConfig{
		ClientSet:    cs,
		ResyncPeriod: 15 * time.Minute,
	}, nil
}

func NewNodeWatcher(nic *NodeInformerConfig, nc NodeConfiguration, target ExportTarget) *NodeWatcher {
	q := queue.NewDelayingQueueStatsd("kubernetes.node_handler")
	nw := &NodeWatcher{
		stopC:             make(chan struct{}),
		wg:                sync.WaitGroup{},
		clientset:         nic.ClientSet,
		queue:             q,
		maxRequeueCount:   nc.MaxRequeueCount,
		target:            target,
		NodeConfiguration: nc,
	}

	sharedInformers := informers.NewSharedInformerFactory(nic.ClientSet, nic.ResyncPeriod)
	nw.informer = sharedInformers.Core().V1().Nodes()
	nw.informer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(obj)
				if err != nil {
					log.Printf("could not get key for object: %v, %v", obj, err)
					return
				}

				nw.queue.Add(NewKeyOp(key, obj, AddOp))
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(newObj)
				if err != nil {
					log.Printf("could not get key for object: %v, %v", newObj, err)
					return
				}

				nw.queue.Add(NewKeyOp(key, newObj, UpdateOp))
			},
			DeleteFunc: func(obj interface{}) {
				key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
				if err != nil {
					log.Printf("could not get key for object: %v, %v", obj, err)
					return
				}

				nw.queue.Add(NewKeyOp(key, obj, DeleteOp))
			}})

	return nw
}

func (nw *NodeWatcher) Run() error {
	nw.wg.Add(1)
	go func() {
		defer nw.wg.Done()
		nw.informer.Informer().Run(nw.stopC)
	}()

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(nw.stopC, nw.informer.Informer().HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return nil
	}

	wait.Until(nw.runWorker, 100*time.Millisecond, nw.stopC)

	return nil
}

func (nw *NodeWatcher) runWorker() {
	for nw.processNextItem() {
	}
}

func (nw *NodeWatcher) processNextItem() bool {
	var node *v1.Node
	var success bool

	// Wait until there is a new item in the working queue
	ko, shutdown := nw.queue.Get()
	if shutdown {
		return false
	}
	defer nw.queue.Done(ko)

	keyOpTyped, ok := ko.(KeyOp)
	if !ok {
		log.Printf("expected keyOp in queue but got %#v", ko)
		nw.queue.Forget(ko)
		return true
	}

	// the key won't be in the store if it's a delete operation, so we can skip this check
	// instead, use the Node object in the queue
	if keyOpTyped.Operation != DeleteOp {
		obj, found, err := nw.informer.Informer().GetIndexer().GetByKey(keyOpTyped.Key)
		if err != nil {
			log.Printf("Error fetching object with key %s from store: %v", keyOpTyped.Key, err)
			nw.queue.Forget(ko)
			return true
		}

		if !found {
			log.Printf("Object with key %s not found in store", keyOpTyped.Key)
			nw.queue.Forget(ko)
			return true
		}

		node, ok = obj.(*v1.Node)
		if !ok {
			log.Printf("expected *v1.Node in queue but got %#v", obj)
			nw.queue.Forget(ko)
			return true
		}
	}

	switch keyOpTyped.Operation {
	case AddOp:
		stats.Client().Incr("kubernetes.node_handler", []string{"handler:add"}, 1.0)
		nw.exportNodes()
		success = true

	case UpdateOp:
		stats.Client().Incr("kubernetes.node_handler", []string{"handler:update"}, 1.0)
		nw.exportNodes()
		success = true

	case DeleteOp:
		stats.Client().Incr("kubernetes.node_handler", []string{"handler:delete"}, 1.0)
		nw.exportNodes()
		success = true
	}

	// Check again after the grace period has expired
	if _, after := util.ShouldInclude(node); after > 0 {
		log.Printf("requeuing Node %s in %v due to GracePeriod still in effect", node.Name, after)

		nw.queue.AddAfter(ko, after)
		return true
	}

	if success {
		nw.queue.Forget(ko)
		return true
	}

	// If we've failed to process the item, we need to retry it
	if numRequeues := nw.queue.NumRequeues(keyOpTyped); numRequeues < nw.maxRequeueCount {
		log.Printf("requeuing Node %s, retry number %d)", node.Name, numRequeues)
		nw.queue.AddRateLimited(ko)
		return true
	}

	return true
}

func (nw *NodeWatcher) Stop() {
	close(nw.stopC)
	// shutdown immediately
	nw.queue.ShutDown()
	// wait until the handlers have completed
	nw.wg.Wait()
}

func (sw *NodeWatcher) String() string {
	return "Kubernetes Node handler"
}

func (nw *NodeWatcher) exportNodes() {
	var readyNodes []*v1.Node

	var selector string
	var selectorLogs []string
	for _, selector = range nw.Selectors {
		labelSelector, err := labels.Parse(selector)
		if err != nil {
			log.Println("Error parsing label selector: ", err)
		}

		nodes, err := nw.informer.Lister().List(labelSelector)
		if err != nil {
			log.Println("Error getting node list: ", err)
		}

		candidateNodes := []*v1.Node{}
		for _, node := range nodes {
			if include, _ := util.ShouldInclude(node); include {
				candidateNodes = append(candidateNodes, node)
			}
		}

		// Always save the last set of nodes we found, even if we don't have
		// enough to satisfy the minNodes requirement.  If we don't find any
		// selectors with enough nodes, we'll use the last one that had nodes.
		// If we find minReady nodes then exit early.
		if len(candidateNodes) > 0 {
			readyNodes = candidateNodes
			if len(readyNodes) > nw.MinCount {
				break
			}
		}

		selectorLogs = append(selectorLogs, fmt.Sprintf("Not enough nodes found for selector %s, found %d, need %d", selector, len(candidateNodes), nw.MinCount))
	}

	if nw.curSelector != selector {
		nw.curSelector = selector
		log.Printf("Using '%s' selector, found %d nodes, needed at least %d", selector, len(readyNodes), nw.MinCount)
		for _, slog := range selectorLogs {
			log.Println(slog)
		}
	}

	for _, node := range readyNodes {
		stats.Client().Gauge("nodes", 1, []string{"selector:" + selector, "node:" + node.Name}, 1.0)
	}

	if err := nw.target.WriteNodes(readyNodes); err != nil {
		log.Println("Error writing nodes to target: ", err)
	}
}
