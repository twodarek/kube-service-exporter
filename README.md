# Archived
This is a public archive of the most recent version of kube-service-exporter before it was closed-sourced at GitHub.  Not actively maintained.

# kube-service-exporter

A Kubernetes controller for enabling load balancing *across multiple clusters*, written in Go.

By exporting Kubernetes Service and Node metadata to Consul, `kube-service-exporter` facilitates bringing an external load balancer to Kubernetes installations for highly available cluster designs.

## Overview

Traffic ingress into a Kubernetes cluster from on-prem or custom load balancers is complex. Commonly, Ingress controllers are used to support this use case, but  this creates an often unnecessary intermediate proxy and TCP/L4 ingress is still poorly supported. `kube-service-exporter` runs inside the cluster and exports metadata about Kubernetes Services and Nodes to Consul which can be used to configure a custom load balancer according to individual needs.  Services exported by `kube-service-exporter` can be configured to load balance parallel deployments of the same service across multiple Kubernetes clusters, allowing Kubernetes clusters to be highly available. Metadata about the configuration is stored in Annotations on the Service resource which mirror the Service Annotations used when configuring cloud load balancers for providers such as AWS and GCE.

`kube-service-exporter` does not actively configure load balancers. It enables dynamic edge load balancing and highly available cluster designs by exporting information about a Kubernetes Service to Consul for consumption by tools like `consul-template`.

## Metadata

`kube-service-exporter` creates the following metadata in Consul:

* In [Consul KV](https://www.consul.io/api/kv.html): A JSON document for each exported Kubernetes Service, grouped by a cluster identifier (from the `KSE_CLUSTER_ID` environment variable)
* In Consul KV: A JSON array of healthy Nodes with their hostname and IP address
* (Optionally) A [Consul Service](https://www.consul.io/docs/agent/services.html) for each Kubernetes Service

### Services (Consul KV)

Metadata is exported to Consul for Kubernetes Services of `type: LoadBalancer` or `type: NodePort` with the `kube-service-exporter.github.com/exported` annotation set to `"true"`.  The exported metadata for each Kubernetes Service is available at `$KSE_KV_PREFIX/services/$SERVICE_NAME/clusters/$CLUSTER_ID`.  This path warrants some explanation:

* `KSE_KV_PREFIX` is the value of the `KSE_KV_PREFIX` environment variable passed in and defaults to `kube-service-exporter`.
* `SERVICE_NAME` is a generated string that uniquely identifies an exported service as `(${CLUSTER_ID}-)${NAMESPACE}-${SERVICE_NAME}-${SERVICE_PORT}`
  * cluster ID (`KSE_CLUSTER_ID` from the environment) **only if `kube-service-exporter.github.com/load-balancer-service-per-cluster` is set to `"true"`**
  * The Kubernetes Namespace (`metadata.namespace`) that the Service is deployed to
  * The Name of the Kubernetes Service (`metadata.name`)
  * The Kubernetes Service port name (`spec.ports[].name`) or port number (`spec.ports[].port`) *if no name is given*. Kubernetes Services with multiple ports exposed will be split into multiple exported services in Consul.
* `CLUSTER_ID` is the value of the `KSE_CLUSTER_ID` environment variable.

Consider a Kubernetes Service as follows:

```yaml
apiVersion: v1
kind: Service
metadata:
  annotations:
   kube-service-exporter.github.com/custom-attrs: |
      {
        "allowed_ips": [
          "10.0.0.0/8",
          "172.16.0.0/12",
          "192.168.0.0/16"
        ]
      }
    kube-service-exporter.github.com/exported: "true"
    kube-service-exporter.github.com/load-balancer-backend-protocol: http
    kube-service-exporter.github.com/load-balancer-class: internal
    kube-service-exporter.github.com/load-balancer-dns-name: robots.example.net
    kube-service-exporter.github.com/load-balancer-service-per-cluster: "false"
  labels:
    service: megatron
  name: megatron
  namespace: decepticons
spec:
  ports:
  - name: http
    nodePort: 32046
    port: 8080
    protocol: TCP
    targetPort: http
  selector:
    app: megatron
  type: LoadBalancer
```

With `KSE_CLUSTER_ID=transformers`, the Kubernetes Service :point_up: would appear in Consul KV at `kube-service-exporter/services/decepticons-megatron-http/clusters/transformers` with the value:

```json
{
  "hash": "80ce408f9e6c914",             // Internal use
  "ClusterName": "transformers",         // The cluster ID for this service
  "port": 32046,                         // The NodePort the Service is available at on each Node on this cluster
  "dns_name": "robots.example.net",      // set by `kube-service-exporter.github.com/load-balancer-dns-name`
  "health_check_path": "/healthz",       // set by `kube-service-exporter.github.com/load-balancer-health-check-path`
  "health_check_port": 32046,            // set by `kube-service-exporter.github.com/load-balancer-health-check-port`
  "backend_protocol": "http",            // set by `kube-service-exporter.github.com/load-balancer-backend-protocol`
  "proxy_protocol": false,               // set by 'kube-service-exporter.github.com/load-balancer-proxy-protocol`
  "load_balancer_class": "internal",     // set by `kube-service-exporter.github.com/load-balancer-class`
  "load_balancer_listen_port": 0,        // set by `kube-service-exporter.github.com/load-balancer-listen-port`
  "custom_attrs": {                      // set by `kube-service-exporter.github.com/custom-attrs`
    "allowed_ips": [
      "10.0.0.0/8",
      "172.16.0.0/12",
      "192.168.0.0/16"
    ]
  }
}

```

See the Annotations section :point_down: for details on how to configure these metadata with annotations on the Kubernetes Service.

### Services (Consul Service)

`kube-service-exporter` can optionally export Consul Services in addition to the Consul KV output :point_up:.  Since the `meta` property of [Consul Services](https://www.consul.io/docs/agent/services.html) is relatively new and has some limitations, it is not used to track Consul Service metadata and the companion KV data should be used instead.

To export Consul Services:

* `kube-service-exporter` must be running as a DaemonSet on every Node in the Kubernetes cluster that will serve requests
* There must be a Consul Agent running on each Kubernetes Node that serves request.
* The environment variable `KSE_SERVICES_ENABLED` must be set to a truthy [value](https://golang.org/pkg/strconv/#ParseBool) (e.g. true, t, 1).

When configured to export Consul Services, the Kubernetes Service above would have an entry in the [Consul Catalog](https://www.consul.io/api/catalog.html) for each cluster Node similar to this:

```json

[
  {
    "Node": "kube-node-1.mydomain.net",
    "Address": "192.168.1.11",
    "TaggedAddresses": {
      "lan": "192.168.1.11",
      "wan": "192.168.1.11"
    },
    "ServiceID": "decepticons-megatron-http",   // The Service Name, which follows the
    "ServiceName": "decepticons-megatron-http", // rules outlined for service names above
    "ServiceTags": [
      "transformers",                           // The Cluster ID
      "kube-service-exporter"                   // All generated Services receive this tag
    ],
    "ServiceAddress": "192.168.1.11",
    "ServicePort": 32046,                       // The Service NodePort
    "ServiceEnableTagOverride": false,
    "CreateIndex": 1248011971,
    "ModifyIndex": 1295067447
  },
  ...
]
```

### Nodes (Consul KV)

Metadata about Kubernetes Nodes in the Kubernetes cluster is stored as a JSON array in Consul KV under `$KSE_KV_PREFIX/nodes/$KSE_CLUSTER_ID`.

```json
$ consul kv get kube-service-exporter/nodes/mycluster
[
  {
    "Name": "kube-node-1.mydomain.net",
    "Address": "192.168.1.11"
  },
  {
    "Name": "kube-node-2.mydomain.net",
    "Address": "192.168.1.12"
  },
  {
    "Name": "kube-node-3.mydomain.net",
    "Address": "192.168.1.13"
  }
]
```

In order for a Node to appear in this list, the following criteria must be met:

* The NodeReady NodeCondition must be true
* The Node matches the label selector specified by the `KSE_NODE_SELECTOR` environment variable

This means when Nodes are drained or unhealthy, they will automatically be removed from Consul, reducing the possibility that a request will be routed to an out-of-service Node.

### "Ingress" Services

In some cases, there could be a different `Service` in-between the load balancer and the target `Service`.  A common example would be an ingress gateway, where the load balancer should be sending traffic to the NodePort for the ingress gateway, not directly to the `Service`. For these cases, there are 3 annotations which all need to be set to identify that Service:

* `kube-service-exporter.github.com/ingress-service-namespace`
* `kube-service-exporter.github.com/ingress-service-name`
* `kube-service-exporter.github.com/ingress-service-port-name`

If the ingress namespace/name/port are all set and the ingress `Service` and port exists, `kube-service-exporter` will replace the `port` and `health_check_port` fields stored in consul with the value from the ingress `Service`.
`kube-service-exporter` will also add an `ingress_sni` field to a `Service`'s metadata in consul.
This field uniquely identifies an exported service (a K8s Service port) and is useful in cases where an ingress gateway
accepts traffic for multiple TCP services on the same port.
A load-balancer can then set `ingress_sni` as an SNI so that ingress gateways can determine to which `Service` a TCP connection should be routed.

### Per-Cluster Load Balancing vs Multi-Cluster Load Balancing

## Annotations

The following Annotations are supported on the Kubernetes Service resource. Note that these Annotations only describe *intent* and their usage is specific to the *consumer* of this metadata (e.g. `haproxy` configured via `consul-template`). As with all Kubernetes Annotations, the values must be strings.

* `kube-service-exporter.github.com/exported` - When set to the string `"true"`, this Kubernetes Service will be exported.  Defaults to `"false"`, which means do not export.
* `kube-service-exporter.github.com/load-balancer-proxy-protocol` - Set to `"*"` to signal that all backends support PROXY Protocol.
* `kube-service-exporter.github.com/load-balancer-class` - The load balancer class is an identifier to associate this Service with a load balancer.  Examples might include "internal", "public", "corp", etc. It is up to the consumer to decide how this field is used.
* `kube-service-exporter.github.com/load-balancer-backend-protocol` - This is used to specify the protocol spoken by the backend Pods behind a load balancer listener. Valid options are `http` or `tcp` for HTTP or TCP backends.
* `kube-service-exporter.github.com/load-balancer-health-check-path` - A path or URI for an HTTP health check
* `kube-service-exporter.github.com/load-balancer-health-check-port` - The port for the Health check. If unset, defaults to the Service NodePort.
* `kube-service-exporter.github.com/load-balancer-service-per-cluster` - If unset (or set to `"false"`), this will create a separately named service *per cluster id*.  This is useful for applications that should *not* be load balanced across multiple clusters.  The default is `"true"`, which will aggregate the same service across different clusters into the same name.  Service uniqueness is defined by a tuple of namespace, name, & port name with an optional cluster id.
* `kube-service-exporter.github.com/load-balancer-dns-name` - The DNS name that should be routed to this Service.
* `kube-service-exporter.github.com/ingress-service-namespace` - The Namespace where the ingress Service exists
* `kube-service-exporter.github.com/ingress-service-name` - The Name of the ingress Service
* `kube-service-exporter.github.com/ingress-service-port-name` - The PortName of the ingress Service
* `kube-service-exporter.github.com/custom-attrs` - An arbitrary JSON object that will be parsed and added to the exported to Consul under `.custom_attrs`

The following Annotations are supported on the Kubernetes Node resource.  As with all Kubernetes Annotations, the values must be strings.

* `kube-service-exporter.github.com/pretend-ready-until` - If set, this Node will be exported to Consul, regardless of its NodeReady NodeCondition until the given RFC3339 timestamp.

## Configuration

Exported services are configured with Annotations on the Service, but `kube-service-exporter` itself is configured with environment variables.  The following configuration options are available:

* `KSE_NODE_SELECTOR` (default: all Nodes) - A [label selector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors) that will be used to determine which nodes are exported to Consul.  Multiple node selectors can be specified by passing in a JSON array of label selectors.  If multiple label selectors are provided, then the first one that has `KSE_MIN_SELECTOR_NODES` available will be used.
* `KSE_MIN_SELECTOR_NODES` (default: 1) - The minimum number of nodes that must match the `KSE_NODE_SELECTOR` in order for the node selector to be used.  If no selector has this many nodes, then the last selector given that has any nodes will be used.
* `KSE_CONSUL_KV_PREFIX` (default: kube-service-exporter) - The prefix to use when setting Consul KV keys.
* `KSE_CONSUL_DATACENTER` (default: Consul Agent Default) - The name of the Consul Datacenter to use
* `KSE_CONSUL_HOST` (default: 127.0.0.1) - The IP or hostname for the Consul Agent.
* `KSE_CONSUL_PORT` (default: 8500) - The HTTP port for the Consul Agent.
* `KSE_DOGSTATSD_ENABLED` (default: true) - Set to "false" to disable sending of dogstatsd metrics
* `KSE_DOGSTATSD_HOST` (default: 127.0.0.1) - The IP or hostname for the Datadog dogstatsd agent
* `KSE_DOGSTATSD_PORT` (default: 8125) - The port for the Datadog dogstatsd agent
* `KSE_DOGSTATSD_TAGS` (default: none) - A comma-separated list of tags to add to all metrics sent to dogstatsd
* `KSE_HTTP_IP` (default: all IPs) - The IP for `kube-service-exporter` to listen on for the `/healthz` endpoint and [go expvar stats](https://golang.org/pkg/expvar/)
* `KSE_HTTP_PORT` (default: 8080) - The port for the health/stats listener
* `KSE_SERVICES_ENABLED` (default: false) - Set to "true" to export Consul Services in addition to Consul KV metadata.  Requires additional configuration described :point_up:
* `KSE_SERVICE_RESYNC_PERIOD` (default: 15) - The service watcher's resync period in minutes. This is the period at which the service watcher will re-queue all Services in the cluster to ensure they are up-to-date.

## Metrics

## Consul Target

Please see an [example deploy here](/examples/deploy-example.md).

### Operation

## How To Use

The current public docker image of kube-service-exporter is available on DockerHub:
`github/kube-service-exporter:latest`

For an example deploy, please see the [Examples folder](/examples/deploy-example.md).

## Contributing

Please see our [contributing document](/CONTRIBUTING.md) if you would like to participate!

## Getting help

If you have a problem or suggestion, please [open an issue](https://github.com/github/kube-service-exporter/issues/new) in this repository, and we will do our best to help. Please note that this project adheres to the [Contributor Covenant Code of Conduct](/CODE_OF_CONDUCT.md).

## License

kube-service-exporter is licensed under the [Apache 2.0 license](LICENSE).

## Maintainers

`kube-service-exporter` was originally designed and authored by [Aaron Brown](https://github.com/aaronbbrown).
It is maintained, reviewed, and tested by the Production Engineering team at GitHub.
Contact the maintainers: opensource+kube-service-exporter@github.com

## Development

A [Tilt](https://tilt.dev/) environment is provided. To start the environment:

- first run `script/bootstrap` (or otherwise make sure the development environment has `kind` and `docker` installed)
- run `script/server` to build a dev cluster and start Tilt

To cleanup, run `script/teardown`, which will destroy the development cluster.
