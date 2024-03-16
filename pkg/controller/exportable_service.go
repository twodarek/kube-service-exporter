package controller

import (
	"encoding/json"
	"fmt"
	"github.com/github/kube-service-exporter/pkg/util"
	"log"
	"strconv"
	"strings"
	"unicode"

	"github.com/mitchellh/hashstructure"
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	// ServiceAnnotationExported is a boolean that determines whether or not to
	// export the service.  Its value is not stored anywhere in the
	// ExportedService, but it is used in IsExportable()
	ServiceAnnotationExported = "kube-service-exporter.github.com/exported"

	// ServiceAnnotationLoadBalancerProxyProtocol is the annotation used on the
	// service to signal that the proxy protocol should be enabled.  Set to
	// "*" to indicate that all backends should support Proxy Protocol.
	ServiceAnnotationProxyProtocol = "kube-service-exporter.github.com/load-balancer-proxy-protocol"

	// The load balancer class is the target load balancer to apply that the
	// service should be a member of.  Examples might be "internal" or "public"
	ServiceAnnotationLoadBalancerClass = "kube-service-exporter.github.com/load-balancer-class"

	// ServiceAnnotationLoadBalancerBEProtocol is the annotation used on the service
	// to specify the protocol spoken by the backend (pod) behind a listener.
	// Options are `http` or `tcp` for HTTP backends or TCP backends
	ServiceAnnotationLoadBalancerBEProtocol = "kube-service-exporter.github.com/load-balancer-backend-protocol"

	// The port the load balancer should listen on for requests routed to this service
	ServiceAnnotationLoadBalancerListenPort = "kube-service-exporter.github.com/load-balancer-listen-port"

	// A path for an HTTP Health check.
	ServiceAnnotationLoadBalancerHealthCheckPath = "kube-service-exporter.github.com/load-balancer-health-check-path"
	// The port for a the Health check. If unset, defaults to the NodePort.
	ServiceAnnotationLoadBalancerHealthCheckPort = "kube-service-exporter.github.com/load-balancer-health-check-port"

	// If set and set to "false" this will create a separate service
	// *per cluster id*, useful for applications that should not be
	// load balanced across multiple clusters.
	ServiceAnnotationLoadBalancerServicePerCluster = "kube-service-exporter.github.com/load-balancer-service-per-cluster"

	ServiceAnnotationLoadBalancerDNSName = "kube-service-exporter.github.com/load-balancer-dns-name"

	// CustomAttrs is like a "junk drawer" - clients can put arbitrary json objects in the annotation, and
	// we'll parse it and make that object available in the consul payload under `.custom_attrs`
	ServiceAnnotationCustomAttrs = "kube-service-exporter.github.com/custom-attrs"

	// IdPrefix allows the service to define a string with which to prefix the
	// Id, allowing control over the sort order of two or more services
	ServiceAnnotationIdPrefix = "kube-service-exporter.github.com/id-prefix"

	// ServiceAnnotationIngressServiceName/Namespace specifies a different Service with ports that will be used
	// for cases such as an Ingress Controller where this Service is not the same as the Service that
	// is exposed to the Load Balancer. In this case, the Service that is exposed to the Load Balancer
	// should be annotated with this annotation, and the value should be the name of the Namespace/Service that
	// is actually being load balanced.
	ServiceAnnotationIngressServiceNamespace = "kube-service-exporter.github.com/ingress-service-namespace"
	ServiceAnnotationIngressServiceName      = "kube-service-exporter.github.com/ingress-service-name"
	ServiceAnnotationIngressServicePortName  = "kube-service-exporter.github.com/ingress-service-port-name"

	defaultPortLabel = "default"
)

type ExportedService struct {
	ClusterId string `json:"ClusterName"`
	Namespace string `json:"-"`
	Name      string `json:"-"`

	// The unique Name for the NodePort. If no name, defaults to the Port
	PortName string `json:"-"`
	// The Port on which the Service is reachable
	Port int32 `json:"port"`

	DNSName           string `json:"dns_name,omitempty"`
	ServicePerCluster bool   `json:"service_per_cluster,omitempty"`

	// an optional URI Path for the HealthCheck
	HealthCheckPath string `json:"health_check_path,omitempty"`

	// HealthCheckPort is a port for the Health Check. Defaults to the NodePort
	HealthCheckPort int32 `json:"health_check_port,omitempty"`

	// TCP / HTTP
	BackendProtocol string `json:"backend_protocol,omitempty"`

	// Enable Proxy protocol on the backend
	ProxyProtocol bool `json:"proxy_protocol,omitempty"`

	// LoadBalancerClass can be used to target the service at a specific load
	// balancer (e.g. "internal", "public"
	LoadBalancerClass string `json:"load_balancer_class,omitempty"`

	// the port the load balancer should listen on
	LoadBalancerListenPort int32 `json:"load_balancer_listen_port,omitempty"`

	// IngressService* can be used to specify a different Service with ports that will be used
	// for cases such as an Ingress Controller where this Service is not the same as the Service that
	// is exposed to the Load Balancer.
	ingressServiceName      string
	ingressServiceNamespace string
	ingressServicePortName  string
	IngressSNI              string `json:"ingress_sni,omitempty"`

	CustomAttrs map[string]interface{} `json:"custom_attrs"`

	// An optional prefix to be added to the generated ExportedService id
	IdPrefix string `json:"-"`

	// Version is a version specifier that can be used to force the Hash function
	// to change and thus rewrite the service metadata. This is useful in cases
	// where the JSON serialization of the object changes, but not the struct
	// itself.
	Version int `json:"-"`
}

// NewExportedServicesFromKubeService returns a slice of ExportedServices, one
// for each v1.Service Port.
func NewExportedServicesFromKubeService(service *v1.Service, clusterId string, indexer cache.Indexer) ([]*ExportedService, error) {
	if !IsExportableService(service) {
		return nil, nil
	}

	exportedServices := make([]*ExportedService, 0, len(service.Spec.Ports))
	for i := range service.Spec.Ports {
		es, err := NewExportedService(service, clusterId, i)
		if err != nil {
			return nil, err
		}
		exportedServices = append(exportedServices, es)
	}

	// If the Service is an Ingress Service, merge in the Service information
	for i, es := range exportedServices {
		if es.ingressServiceNamespace == "" || es.ingressServiceName == "" || es.ingressServicePortName == "" {
			continue
		}

		// look up the service to get the ports
		obj, exists, err := indexer.GetByKey(es.ingressServiceNamespace + "/" + es.ingressServiceName)
		if err != nil {
			return nil,
				fmt.Errorf("error looking up service %s/%s: %s",
					es.ingressServiceNamespace,
					es.ingressServiceName,
					err)
		}

		if !exists {
			return nil,
				fmt.Errorf("ingress service %s/%s not found for service %s/%s, skipping",
					es.ingressServiceNamespace,
					es.ingressServiceName,
					es.Namespace,
					es.Name)
		}

		ingressSvc, ok := obj.(*v1.Service)
		if !ok {
			return nil,
				fmt.Errorf("ingress service %s/%s is not a v1.Service, skipping",
					es.ingressServiceNamespace,
					es.ingressServiceName)
		}

		_, explicitHCPort := service.Annotations[ServiceAnnotationLoadBalancerHealthCheckPort]
		if exportedServices[i].MergeIngressService(ingressSvc, explicitHCPort) {
			if err := es.setIngressSNI(); err != nil {
				return nil,
					fmt.Errorf("error setting IngressSNI for service %s/%s: %w",
						es.Namespace,
						es.Name,
						err)
			}

			log.Printf("merged ingress service %s/%s into service %s/%s",
				es.ingressServiceNamespace,
				es.ingressServiceName,
				es.Namespace,
				es.Name)
		}
	}

	return exportedServices, nil
}

// An Id for the Service, which allows cross-cluster grouped services
// If two services share the same Id on different clusters, the Service will
// be namespaced based on the Tag below, so it can be differentiated.
func (es *ExportedService) Id() string {
	var sb strings.Builder

	if es.IdPrefix != "" {
		sb.WriteString(es.IdPrefix + "-")
	}

	if es.ServicePerCluster {
		sb.WriteString(es.ClusterId + "-")
	}

	fmt.Fprintf(&sb, "%s-%s-%s", es.Namespace, es.Name, es.PortName)

	return sb.String()
}

// MergeIngressService merges in Service information from the specified Ingress Service
// and port name. This is useful for cases where the service is exposed to a load
// balancer via an ingress controller or intermediate load balancer.
// Returns true if the port was found and merged in, false otherwise.
func (es *ExportedService) MergeIngressService(svc *v1.Service, explicitHCPort bool) bool {
	// update the ports on the exported service with the ports from the ingress service
	for _, port := range svc.Spec.Ports {
		if port.Name == es.ingressServicePortName {
			es.Port = port.NodePort
			if !explicitHCPort {
				es.HealthCheckPort = port.NodePort
			}
			return true
		}
	}

	return false
}

// NewExportedService takes in a v1.Service and an index into the
// v1.Service.Ports array and returns an ExportedService.
func NewExportedService(service *v1.Service, clusterId string, portIdx int) (*ExportedService, error) {
	// TODO add some validation to make sure that the clusterId contains only
	//      safe characters for Consul Service names
	if clusterId == "" {
		return nil, errors.New("no clusterId specified")
	}

	es := &ExportedService{
		Namespace:         service.Namespace,
		Name:              service.Name,
		PortName:          service.Spec.Ports[portIdx].Name,
		Port:              service.Spec.Ports[portIdx].NodePort,
		HealthCheckPort:   service.Spec.Ports[portIdx].NodePort,
		ServicePerCluster: true,
		BackendProtocol:   "http",
		ClusterId:         clusterId,
		Version:           1,
	}

	if es.PortName == "" {
		// use the container port since it will be consistent across clusters
		// and the NodePort will not.
		es.PortName = strconv.Itoa(int(service.Spec.Ports[portIdx].Port))
	}

	if service.Annotations == nil {
		return es, nil
	}

	if val, ok := service.Annotations[ServiceAnnotationLoadBalancerDNSName]; ok {
		es.DNSName = val
	}

	if service.Annotations[ServiceAnnotationProxyProtocol] == "*" {
		es.ProxyProtocol = true
	}

	if val, ok := service.Annotations[ServiceAnnotationLoadBalancerClass]; ok {
		es.LoadBalancerClass = val
	}

	if service.Annotations[ServiceAnnotationLoadBalancerBEProtocol] == "tcp" {
		es.BackendProtocol = "tcp"
	}

	if val, ok := service.Annotations[ServiceAnnotationLoadBalancerListenPort]; ok {
		port, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("error setting LoadBalancerListenPort: %v", err)
		}
		es.LoadBalancerListenPort = int32(port)
	}

	if val, ok := service.Annotations[ServiceAnnotationLoadBalancerHealthCheckPath]; ok {
		es.HealthCheckPath = val
	}

	if val, ok := service.Annotations[ServiceAnnotationLoadBalancerHealthCheckPort]; ok {
		port, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("error setting HealthCheckPort: %v", err)
		}
		es.HealthCheckPort = int32(port)
	}

	if val, ok := service.Annotations[ServiceAnnotationLoadBalancerServicePerCluster]; ok {
		parsed, err := strconv.ParseBool(val)
		if err != nil {
			return nil, errors.Wrap(err, "Error setting ServicePerCluster")
		}
		es.ServicePerCluster = parsed
	}

	if val, ok := service.Annotations[ServiceAnnotationCustomAttrs]; ok {
		var customAttrs map[string]interface{}
		err := json.Unmarshal([]byte(val), &customAttrs)
		if err != nil {
			return nil, errors.Wrapf(err, "Error parsing customattrs JSON object")
		}

		es.CustomAttrs = customAttrs
	} else {
		es.CustomAttrs = map[string]interface{}{}
	}

	if val, ok := service.Annotations[ServiceAnnotationIdPrefix]; ok {
		es.IdPrefix = val
	}

	if val, ok := service.Annotations[ServiceAnnotationIngressServiceNamespace]; ok {
		es.ingressServiceNamespace = val
	}

	if val, ok := service.Annotations[ServiceAnnotationIngressServiceName]; ok {
		es.ingressServiceName = val
	}

	if val, ok := service.Annotations[ServiceAnnotationIngressServicePortName]; ok {
		es.ingressServicePortName = val
	}

	return es, nil
}

func (es *ExportedService) Hash() (string, error) {
	hash, err := hashstructure.Hash(es, nil)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(hash, 16), nil
}

// IsExportableService returns true if:
//   - the target Service has the kube-service-exporter.github.com/exported
//     annotation set to a value which evalutes to a boolean (e.g. "true", "1")
//
// AND
// - is a type: LoadBalancer or type: NodePort Service.
func IsExportableService(service *v1.Service) bool {
	var exported bool

	if val, ok := service.Annotations[ServiceAnnotationExported]; ok {
		parsed, err := strconv.ParseBool(val)
		if err != nil {
			return false
		}
		exported = parsed
	}

	if !exported {
		return false
	}

	return service.Spec.Type == v1.ServiceTypeLoadBalancer ||
		service.Spec.Type == v1.ServiceTypeNodePort
}

func (es *ExportedService) MarshalJSON() ([]byte, error) {
	// alias to avoid recursive marshaling
	type ExportedServiceMarshal ExportedService

	hash, err := es.Hash()
	if err != nil {
		return nil, errors.Wrap(err, "Error generating hash during JSON Marshaling")
	}

	data := struct {
		Hash string `json:"hash"`
		ExportedServiceMarshal
	}{
		Hash:                   hash,
		ExportedServiceMarshal: ExportedServiceMarshal(*es),
	}
	return json.Marshal(&data)
}

func (es *ExportedService) setIngressSNI() error {
	portLabel := es.PortName
	// If we have defaulted the port name to a number, we need to replace it with a valid DNS label
	// this is safe, since K8s requires a port name if there are multiple ports on a Service
	if unicode.IsDigit(rune(es.PortName[0])) {
		portLabel = defaultPortLabel
	}
	ingressSNI := fmt.Sprintf("%s.%s.%s.local", portLabel, es.Name, es.Namespace)
	if !util.IsDomainName(ingressSNI) {
		return fmt.Errorf("ingress_sni is not a valid domain name: %s", ingressSNI)
	}

	es.IngressSNI = ingressSNI

	return nil
}
