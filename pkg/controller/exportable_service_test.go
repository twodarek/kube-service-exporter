package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ServiceFixture() *v1.Service {
	return &v1.Service{
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
					Port:     80,
					NodePort: 32123},
				{
					Name:     "thing",
					Port:     1234,
					NodePort: 32124},
			},
		},
	}
}

func ingressServiceFixture() v1.Service {
	return v1.Service{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "ingress-svc",
			Namespace: "ingress-namespace",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:     "ingress-port",
					Port:     12345,
					NodePort: 33333,
				},
			},
		},
	}
}

func TestIsExportableService(t *testing.T) {
	t.Run("Service is exportable", func(t *testing.T) {
		svc := ServiceFixture()
		assert.True(t, IsExportableService(svc))
	})

	t.Run("ClusterIP Service is not exportable", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Spec.Type = v1.ServiceTypeClusterIP
		assert.False(t, IsExportableService(svc))
	})

	t.Run("NodePort Service is exportable", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Spec.Type = v1.ServiceTypeNodePort
		assert.True(t, IsExportableService(svc))
	})

	t.Run("Service w/out exported annotation is not exportable", func(t *testing.T) {
		svc := ServiceFixture()
		delete(svc.Annotations, ServiceAnnotationExported)
		assert.False(t, IsExportableService(svc))
	})

	t.Run("Service with exported annotation set to false is not exportable", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationExported] = "false"
		assert.False(t, IsExportableService(svc))
	})

	t.Run("Service with non-bool exported annotation is not exportable", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationExported] = "pants"
		assert.False(t, IsExportableService(svc))
	})
}

func TestNewExportedService(t *testing.T) {
	t.Run("Bad inputs", func(t *testing.T) {
		svc := ServiceFixture()
		_, err := NewExportedService(svc, "", 0)
		assert.Error(t, err)
	})

	t.Run("Correct defaults", func(t *testing.T) {
		portIdx := 0
		svc := ServiceFixture()
		es, err := NewExportedService(svc, "cluster", portIdx)

		assert.NoError(t, err)
		assert.Equal(t, "cluster", es.ClusterId)
		assert.Equal(t, svc.Namespace, es.Namespace)
		assert.Equal(t, svc.Name, es.Name)
		assert.Equal(t, svc.Spec.Ports[portIdx].NodePort, es.Port)
		assert.Equal(t, svc.Spec.Ports[portIdx].NodePort, es.HealthCheckPort)
		assert.False(t, es.ProxyProtocol)
		assert.Empty(t, es.LoadBalancerClass)
		assert.Equal(t, "http", es.BackendProtocol)
		assert.Empty(t, es.HealthCheckPath)
		assert.True(t, es.ServicePerCluster)
		assert.Empty(t, es.LoadBalancerListenPort)
		assert.Empty(t, es.CustomAttrs)
	})

	t.Run("Overridden defaults", func(t *testing.T) {
		portIdx := 0
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationProxyProtocol] = "*"
		svc.Annotations[ServiceAnnotationLoadBalancerClass] = "internal"
		svc.Annotations[ServiceAnnotationLoadBalancerBEProtocol] = "tcp"
		svc.Annotations[ServiceAnnotationLoadBalancerHealthCheckPath] = "/foo/bar"
		svc.Annotations[ServiceAnnotationLoadBalancerHealthCheckPort] = "32001"
		svc.Annotations[ServiceAnnotationLoadBalancerServicePerCluster] = "false"
		svc.Annotations[ServiceAnnotationLoadBalancerListenPort] = "32768"
		svc.Annotations[ServiceAnnotationCustomAttrs] = `{"foo":"bar", "baz":["qux"]}`
		svc.Annotations[ServiceAnnotationIngressServiceNamespace] = "ingress-namespace"
		svc.Annotations[ServiceAnnotationIngressServiceName] = "ingress-svc"
		svc.Annotations[ServiceAnnotationIngressServicePortName] = "ingress-port"

		es, err := NewExportedService(svc, "cluster", portIdx)
		assert.NoError(t, err)
		assert.True(t, es.ProxyProtocol)
		assert.Equal(t, "tcp", es.BackendProtocol)
		assert.Equal(t, "/foo/bar", es.HealthCheckPath)
		assert.Equal(t, int32(32001), es.HealthCheckPort)
		assert.False(t, es.ServicePerCluster)
		assert.Equal(t, "internal", es.LoadBalancerClass)
		assert.Equal(t, int32(32768), es.LoadBalancerListenPort)
		assert.Equal(t, map[string]interface{}{"foo": "bar", "baz": []interface{}{"qux"}}, es.CustomAttrs)
		assert.Equal(t, "ingress-namespace", es.ingressServiceNamespace)
		assert.Equal(t, "ingress-svc", es.ingressServiceName)
		assert.Equal(t, "ingress-port", es.ingressServicePortName)
	})

	t.Run("Malformed ServicePerCluster Annotation", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationLoadBalancerServicePerCluster] = "pants"
		_, err := NewExportedService(svc, "cluster", 0)
		assert.Error(t, err)
	})
}

func TestNewExportedServicesFromKubeServices(t *testing.T) {
	t.Run("LoadBalancer", func(t *testing.T) {
		svc := ServiceFixture()
		exportedServices, err := NewExportedServicesFromKubeService(svc, "cluster", nil)
		assert.NoError(t, err)
		assert.Len(t, exportedServices, len(svc.Spec.Ports))

		for i := range svc.Spec.Ports {
			assert.Equal(t, svc.Name, exportedServices[i].Name)
			assert.Equal(t, svc.Spec.Ports[i].NodePort, exportedServices[i].Port)
		}
	})

	t.Run("Unexportable Service Type", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Spec.Type = v1.ServiceTypeClusterIP
		es, err := NewExportedServicesFromKubeService(svc, "cluster", nil)
		assert.NoError(t, err)
		assert.Nil(t, es)
	})

	t.Run("Invalid clusterId", func(t *testing.T) {
		svc := ServiceFixture()
		_, err := NewExportedServicesFromKubeService(svc, "", nil)
		assert.ErrorContains(t, err, "no clusterId specified")
	})

	t.Run("IngressSNI, Port, HealthCheckPort", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationIngressServiceNamespace] = "ingress-namespace"
		svc.Annotations[ServiceAnnotationIngressServiceName] = "ingress-svc"
		svc.Annotations[ServiceAnnotationIngressServicePortName] = "ingress-port"
		ingressSvc := ingressServiceFixture()
		mI := mockIndexer{services: map[string]*v1.Service{"ingress-namespace/ingress-svc": &ingressSvc}}

		ess, err := NewExportedServicesFromKubeService(svc, "cluster", mI)
		assert.NoError(t, err)

		assert.Equal(t, int32(33333), ess[0].Port)
		assert.Equal(t, int32(33333), ess[0].HealthCheckPort)
		assert.Equal(t, "http.service1.default.local", ess[0].IngressSNI)
	})

	t.Run("No port name", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Spec.Ports = []v1.ServicePort{
			{
				Port:     80,
				NodePort: 32123},
		}
		svc.Annotations[ServiceAnnotationIngressServiceNamespace] = "ingress-namespace"
		svc.Annotations[ServiceAnnotationIngressServiceName] = "ingress-svc"
		svc.Annotations[ServiceAnnotationIngressServicePortName] = "ingress-port"
		ingressSvc := ingressServiceFixture()
		mI := mockIndexer{services: map[string]*v1.Service{"ingress-namespace/ingress-svc": &ingressSvc}}

		ess, err := NewExportedServicesFromKubeService(svc, "cluster", mI)
		assert.NoError(t, err)

		assert.Equal(t, int32(33333), ess[0].Port)
		assert.Equal(t, "default.service1.default.local", ess[0].IngressSNI)
	})

	t.Run("invalid port name", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Spec.Ports = []v1.ServicePort{
			{
				Name:     "toolooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooong",
				Port:     80,
				NodePort: 32123},
		}
		svc.Annotations[ServiceAnnotationIngressServiceNamespace] = "ingress-namespace"
		svc.Annotations[ServiceAnnotationIngressServiceName] = "ingress-svc"
		svc.Annotations[ServiceAnnotationIngressServicePortName] = "ingress-port"
		ingressSvc := ingressServiceFixture()
		mI := mockIndexer{services: map[string]*v1.Service{"ingress-namespace/ingress-svc": &ingressSvc}}

		_, err := NewExportedServicesFromKubeService(svc, "cluster", mI)
		assert.ErrorContains(t, err, "not a valid domain name")
	})

	t.Run("Don't override health-check port", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationIngressServiceNamespace] = "ingress-namespace"
		svc.Annotations[ServiceAnnotationIngressServiceName] = "ingress-svc"
		svc.Annotations[ServiceAnnotationIngressServicePortName] = "ingress-port"
		svc.Annotations[ServiceAnnotationLoadBalancerHealthCheckPort] = "12345"
		ingressSvc := ingressServiceFixture()
		mI := mockIndexer{services: map[string]*v1.Service{"ingress-namespace/ingress-svc": &ingressSvc}}

		ess, err := NewExportedServicesFromKubeService(svc, "cluster", mI)
		assert.NoError(t, err)

		assert.Equal(t, int32(33333), ess[0].Port)
		assert.Equal(t, int32(12345), ess[0].HealthCheckPort)
	})

}

func TestId(t *testing.T) {
	t.Run("Id includes cluster and port name", func(t *testing.T) {
		svc := ServiceFixture()
		es, _ := NewExportedService(svc, "cluster", 0)
		assert.Equal(t, "cluster-default-service1-http", es.Id())
	})

	t.Run("Id does not include cluster", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationLoadBalancerServicePerCluster] = "false"
		es, _ := NewExportedService(svc, "cluster", 0)
		assert.Equal(t, "default-service1-http", es.Id())
	})

	t.Run("Id includes cluster and port number", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Spec.Ports[0].Name = ""
		es, _ := NewExportedService(svc, "cluster", 0)
		assert.Equal(t, "cluster-default-service1-80", es.Id())
	})

	t.Run("Id includes prefix", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationIdPrefix] = "myprefix"
		es, _ := NewExportedService(svc, "cluster", 0)
		assert.Equal(t, "myprefix-cluster-default-service1-http", es.Id())
	})

	t.Run("Id includes prefix and not cluster", func(t *testing.T) {
		svc := ServiceFixture()
		svc.Annotations[ServiceAnnotationIdPrefix] = "myprefix"
		svc.Annotations[ServiceAnnotationLoadBalancerServicePerCluster] = "false"
		es, _ := NewExportedService(svc, "cluster", 0)
		assert.Equal(t, "myprefix-default-service1-http", es.Id())
	})
}

func TestHash(t *testing.T) {
	es1, _ := NewExportedService(ServiceFixture(), "cluster", 0)
	hash1, err := es1.Hash()
	assert.Nil(t, err)
	assert.NotEmpty(t, hash1)

	es2, _ := NewExportedService(ServiceFixture(), "cluster", 0)
	hash2, err := es2.Hash()
	assert.Nil(t, err)
	assert.NotEmpty(t, hash2)

	assert.Equal(t, hash1, hash2, "identical ExportedServices should have same hash")

	es2.Port += 1
	hash3, err := es2.Hash()
	assert.Nil(t, err)
	assert.NotEqual(t, hash2, hash3, "different ExportedServices should have different hashes")
}

func TestJSON(t *testing.T) {
	es, _ := NewExportedService(ServiceFixture(), "cluster", 0)
	b, err := json.Marshal(es)
	assert.NoError(t, err)
	// NOTE: The hash in this string will change if the ServiceFixture or
	// type ExportedService changes.
	expected := `{ "service_per_cluster": true,
					"health_check_port": 32123,
					"hash": "14b57b8383342234",
					"ClusterName": "cluster",
					"port": 32123,
					"custom_attrs": {},
					"backend_protocol": "http" }`

	assert.JSONEq(t, expected, string(b))
}
