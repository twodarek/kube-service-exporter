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
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

type ServiceWatcher struct {
	informer  cache.SharedIndexInformer
	stopC     chan struct{}
	wg        sync.WaitGroup
	clusterId string
	queue     queue.Interface
	// cacheOlds is a  to store the old versions of services so we can compare
	// them to the new versions when they are updated
	cacheOlds    map[string]*v1.Service
	mutex        sync.Mutex
	exportTarget ExportTarget
}

type InformerConfig struct {
	ClientSet     kubernetes.Interface
	ListerWatcher cache.ListerWatcher
	ResyncPeriod  time.Duration
}

func NewClientSet() (kubernetes.Interface, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

func NewInformerConfig(resyncPeriod int) (*InformerConfig, error) {
	cs, err := NewClientSet()
	if err != nil {
		return nil, err
	}

	return &InformerConfig{
		ClientSet:    cs,
		ResyncPeriod: time.Duration(resyncPeriod) * time.Minute,
	}, nil
}

func NewServiceWatcher(config *InformerConfig, namespaces []string, clusterId string, target ExportTarget, metrics queue.Metrics) *ServiceWatcher {
	q := queue.NewPriorityQueueWithMetrics(metrics)
	sw := &ServiceWatcher{
		stopC:        make(chan struct{}),
		wg:           sync.WaitGroup{},
		clusterId:    clusterId,
		queue:        &q,
		exportTarget: target,
		cacheOlds:    make(map[string]*v1.Service),
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		config.ClientSet,
		config.ResyncPeriod,
		informers.WithNamespace(meta_v1.NamespaceAll),
	)

	sw.informer = informerFactory.Core().V1().Services().Informer()
	sw.informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(obj)
				if err != nil {
					log.Printf("could not get key for object: %v, %v", obj, err)
					return
				}

				svc, ok := obj.(*v1.Service)
				if !ok {
					log.Println("AddFunc received invalid Service: ", obj)
					return
				}

				// ignore namespaces we don't care about
				if len(namespaces) > 0 && !util.StringInSlice(svc.Namespace, namespaces) {
					return
				}
				sw.queue.Add(NewKeyOp(key, obj, AddOp), 0)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				key, err := cache.MetaNamespaceKeyFunc(newObj)
				if err != nil {
					log.Printf("could not get key for object: %v, %v", newObj, err)
					return
				}

				oldSvc, ok := oldObj.(*v1.Service)
				if !ok {
					log.Println("UpdateFunc received invalid Service: ", oldObj)
					return
				}

				// ignore namespaces we don't care about
				if len(namespaces) > 0 && !util.StringInSlice(oldSvc.Namespace, namespaces) {
					return
				}

				// store the old service in the cache
				sw.mutex.Lock()
				sw.cacheOlds[key] = oldSvc
				sw.mutex.Unlock()

				sw.queue.Add(NewKeyOp(key, newObj, UpdateOp), 0)
			},
			DeleteFunc: func(obj interface{}) {
				key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
				if err != nil {
					log.Printf("could not get key for object: %v, %v", obj, err)
					return
				}

				svc, ok := obj.(*v1.Service)
				if !ok {
					log.Println("DeleteFunc received invalid Service: ", svc)
					return
				}

				// ignore namespaces we don't care about
				if len(namespaces) > 0 && !util.StringInSlice(svc.Namespace, namespaces) {
					return
				}
				sw.queue.Add(NewKeyOp(key, obj, DeleteOp), 0)
			}})

	return sw
}

func (sw *ServiceWatcher) Run() error {
	sw.wg.Add(1)
	go func() {
		defer sw.wg.Done()
		sw.informer.Run(sw.stopC)
	}()

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(sw.stopC, sw.informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return nil
	}

	wait.Until(sw.runWorker, 100*time.Millisecond, sw.stopC)

	return nil
}

func (sw *ServiceWatcher) Stop() {
	close(sw.stopC)
	// wait until the handlers have completed
	sw.wg.Wait()
}

func (sw *ServiceWatcher) String() string {
	return "Kubernetes Service handler"
}

func (sw *ServiceWatcher) runWorker() {
	for sw.processNextItem() {
	}
}

func (sw *ServiceWatcher) processNextItem() bool {
	var svc *v1.Service

	// Wait until there is a new item in the working queue
	ko, err := sw.queue.Get()
	if err != nil {
		return false
	}

	keyOpTyped, ok := ko.(KeyOp)
	if !ok {
		log.Printf("expected keyOp in queue but got %#v", ko)
		sw.queue.Forget(keyOpTyped)
		return true
	}

	// the key won't be in the store if it's a delete operation, so we can skip this check
	// instead, use the Service object in the queue
	if keyOpTyped.Operation != DeleteOp {
		var ok bool

		obj, found, err := sw.informer.GetIndexer().GetByKey(keyOpTyped.Key)
		if err != nil {
			log.Printf("Error fetching object with key %s from store: %v", keyOpTyped.Key, err)
			sw.queue.Forget(keyOpTyped)
			return true
		}

		if !found {
			log.Printf("Object with key %s not found in store", keyOpTyped.Key)
			sw.queue.Forget(keyOpTyped)
			return true
		}

		if svc, ok = obj.(*v1.Service); !ok {
			log.Println("processNextItem received invalid Service: ", obj)
			return true
		}
	}

	success := true
	switch keyOpTyped.Operation {
	case AddOp:
		if err := sw.addService(svc); err != nil {
			log.Println(err)
			success = false
		}

	case UpdateOp:
		sw.mutex.Lock()
		oldSvc := sw.cacheOlds[keyOpTyped.Key]
		sw.mutex.Unlock()

		if err := sw.updateService(oldSvc, svc); err != nil {
			log.Println(err)
			success = false
		}

		if success {
			sw.mutex.Lock()
			delete(sw.cacheOlds, keyOpTyped.Key)
			sw.mutex.Unlock()
		}

	case DeleteOp:
		sw.deleteService(keyOpTyped.Obj.(*v1.Service))
	}

	if success {
		sw.queue.Forget(&keyOpTyped)
	}

	return true
}

func (sw *ServiceWatcher) addService(service *v1.Service) error {
	start := time.Now()
	defer sw.wg.Done()
	sw.wg.Add(1)
	tags := []string{"handler:add"}
	stats.Client().Incr("kubernetes.service_handler.count", tags, 1.0)
	defer stats.Client().Timing("kubernetes.service_handler.time", time.Since(start), tags, 1.0)

	if !IsExportableService(service) {
		return nil
	}
	exportedServices, err := NewExportedServicesFromKubeService(service, sw.clusterId, sw.informer.GetIndexer())
	if err != nil {
		log.Printf("Error creating exported service from %s/%s: %s", service.Namespace, service.Name, err)

		return err
	}

	for _, es := range exportedServices {
		log.Printf("Add service %s", es.Id())
		_, err := sw.exportTarget.Create(es)
		stats.IncrSuccessOrFail(err, "target.service", []string{"handler:create", "service:" + es.Id()})
		if err != nil {
			log.Printf("error adding %+v, error: %v", es, err)
		}
	}

	return nil
}

func (sw *ServiceWatcher) updateService(oldService *v1.Service, newService *v1.Service) error {
	start := time.Now()
	defer sw.wg.Done()
	sw.wg.Add(1)
	tags := []string{"handler:update"}
	stats.Client().Incr("kubernetes.service_handler.count", tags, 1.0)
	defer stats.Client().Timing("kubernetes.service_handler.time", time.Since(start), tags, 1.0)

	// Delete services that are not exportable (because they aren't
	// LoadBalancer, NodePort, or opt-in)
	if !IsExportableService(newService) {
		// delete the
		sw.deleteService(oldService)
	}

	newIds := make(map[string]bool)

	oldExportedServices, _ := NewExportedServicesFromKubeService(oldService, sw.clusterId, sw.informer.GetIndexer())
	newExportedServices, err := NewExportedServicesFromKubeService(newService, sw.clusterId, sw.informer.GetIndexer())
	if err != nil {
		log.Printf("Error creating exported service during update of %s/%s: %s", newService.Namespace, newService.Name, err)

		return err
	}

	for _, es := range newExportedServices {
		// attempt to figure out which service this used to be to help reduce
		// the probability for orphans occurring. For the consul target, if both
		// the PortName AND Consul key name change at the same time AND there
		// is more than one port in the K8s Service, then orphans are
		// unavoidable.
		oldES := sw.matchingExportedService(oldExportedServices, es)
		if oldES == nil && len(oldExportedServices) == 1 && len(newExportedServices) == 1 {
			oldES = oldExportedServices[0]
		}

		newIds[es.Id()] = true
		log.Printf("Update service %s", es.Id())
		_, err := sw.exportTarget.Update(oldES, es)
		stats.IncrSuccessOrFail(err, "target.service", []string{"handler:update", "service:" + es.Id()})
		if err != nil {
			return fmt.Errorf("error updating %+v: %w", es, err)
		}
	}

	// delete ExportedServices that are in old, but not new (by Id)
	// This should cover renaming the port name, or a change in other metadata
	// such as ServicePerCluster
	for _, es := range oldExportedServices {
		if _, ok := newIds[es.Id()]; !ok {
			log.Printf("Delete service %+v due to Id change", es)
			sw.exportTarget.Delete(es)
		}
	}
	return nil
}

// matchingExportedService finds and returns the matching ExportedService within
// a list and returns it. An ExportedService is considered matching if the
// calculated IDs match. Returns nil if no match
func (sw *ServiceWatcher) matchingExportedService(list []*ExportedService, es *ExportedService) *ExportedService {
	for i := range list {
		if es.Id() == list[i].Id() {
			return list[i]
		}
	}
	return nil
}

func (sw *ServiceWatcher) deleteService(service *v1.Service) {
	start := time.Now()
	defer sw.wg.Done()
	sw.wg.Add(1)
	tags := []string{"handler:delete"}
	stats.Client().Incr("kubernetes.service_handler.count", tags, 1.0)
	defer stats.Client().Timing("kubernetes.service_handler.time", time.Since(start), tags, 1.0)

	exportedServices, _ := NewExportedServicesFromKubeService(service, sw.clusterId, sw.informer.GetIndexer())

	for _, es := range exportedServices {
		log.Printf("Delete service %s", es.Id())
		_, err := sw.exportTarget.Delete(es)
		stats.IncrSuccessOrFail(err, "target.service", []string{"handler:delete", "service:" + es.Id()})
		if err != nil {
			log.Printf("Error deleting %+v", es)
		}
	}
}
