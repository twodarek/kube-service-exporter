package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/github/kube-service-exporter/pkg/controller"
	"github.com/github/kube-service-exporter/pkg/leader"
	"github.com/github/kube-service-exporter/pkg/queue"
	"github.com/github/kube-service-exporter/pkg/server"
	"github.com/github/kube-service-exporter/pkg/stats"
	"github.com/github/kube-service-exporter/pkg/util"
	capi "github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

var (
	// Build arguments, set at build-time w/ ldflags -X
	GitCommit string
	GitBranch string
	BuildTime string
)

type RunStopper interface {
	Run() error
	Stop()
	String() string
}

func main() {
	viper.SetEnvPrefix("KSE")
	viper.AutomaticEnv()
	viper.SetDefault("CONSUL_KV_PREFIX", "kube-service-exporter")
	viper.SetDefault("CONSUL_DATACENTER", "")
	viper.SetDefault("CONSUL_HOST", "127.0.0.1")
	viper.SetDefault("CONSUL_PORT", 8500)
	viper.SetDefault("DOGSTATSD_ENABLED", true)
	viper.SetDefault("DOGSTATSD_HOST", "127.0.0.1")
	viper.SetDefault("DOGSTATSD_PORT", 8125)
	viper.SetDefault("HTTP_IP", "")
	viper.SetDefault("HTTP_PORT", 8080)
	viper.SetDefault("SERVICES_ENABLED", false)
	viper.SetDefault("MIN_SELECTOR_NODES", 1)
	viper.SetDefault("MAX_REQUEUE_COUNT", 5)
	viper.SetDefault("SERVICE_RESYNC_PERIOD", 15)

	namespaces := viper.GetStringSlice("NAMESPACE_LIST")
	clusterId := viper.GetString("CLUSTER_ID")
	kvPrefix := viper.GetString("CONSUL_KV_PREFIX")
	consulHost := viper.GetString("CONSUL_HOST")
	consulPort := viper.GetInt("CONSUL_PORT")
	consulDatacenter := viper.GetString("CONSUL_DATACENTER")
	podName := viper.GetString("POD_NAME")
	nodeSelector := viper.GetString("NODE_SELECTOR")
	minSelectorNodes := viper.GetInt("MIN_SELECTOR_NODES")
	maxRequeueCount := viper.GetInt("MAX_REQUEUE_COUNT")
	dogstatsdEnabled := viper.GetBool("DOGSTATSD_ENABLED")
	dogstatsdHost := viper.GetString("DOGSTATSD_HOST")
	dogstatsdPort := viper.GetInt("DOGSTATSD_PORT")
	dogstatsdTags := viper.GetStringMapString("DOGSTATSD_TAGS")
	httpIp := viper.GetString("HTTP_IP")
	httpPort := viper.GetInt("HTTP_PORT")
	servicesEnabled := viper.GetBool("SERVICES_ENABLED")
	servicesKeyTemplate := viper.GetString("SERVICES_KEY_TEMPLATE")
	serviceResyncPeriod := viper.GetInt("SERVICE_RESYNC_PERIOD")

	stopTimeout := 10 * time.Second
	stoppedC := make(chan struct{})

	log.Printf("Starting kube-service-exporter: built at: %s, git commit: %s, git branch: %s", BuildTime, GitCommit, GitBranch)

	if !viper.IsSet("CLUSTER_ID") {
		log.Fatalf("Please set the KSE_CLUSTER_ID environment variable to a unique cluster Id")
	}

	if len(namespaces) > 0 {
		log.Printf("Watching the following namespaces: %+v", namespaces)
	}

	nodeSelectors, err := util.ParseNodeSelectors(nodeSelector)
	if err != nil {
		log.Fatalf("Error parsing node selectors: %v", err)
	}

	if dogstatsdEnabled {
		if err := stats.Configure("kube-service-exporter.", dogstatsdHost, dogstatsdPort, dogstatsdTags); err != nil {
			log.Fatal(errors.Wrap(err, "Error configuring dogstatsd"))
		}
	}
	stats.Client().Gauge("start", 1, []string{}, 1.0)

	ic, err := controller.NewInformerConfig(serviceResyncPeriod)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Error setting up Service Watcher"))
	}

	nodeIC, err := controller.NewNodeInformerConfig()
	if err != nil {
		log.Fatal(errors.Wrap(err, "Error setting up Node Watcher"))
	}

	// Get the IP for the local consul agent since we need it in a few places
	consulIPs, err := net.LookupIP(consulHost)
	if err != nil {
		log.Fatal(errors.Wrapf(err, "Error looking up IP for Consul host: %s", consulHost))
	}

	consulLeaderCfg := capi.DefaultConfig()
	consulTargetCfg := capi.DefaultConfig()
	consulAddress := fmt.Sprintf("%s:%d", consulIPs[0].String(), consulPort)
	consulLeaderCfg.Address = consulAddress
	consulTargetCfg.Address = consulAddress
	log.Printf("Using Consul agent at %s", consulAddress)

	if viper.IsSet("CONSUL_DATACENTER") {
		consulTargetCfg.Datacenter = consulDatacenter
		log.Printf("Using Consul datacenter for services %s", consulDatacenter)
	}

	elector, err := leader.NewConsulLeaderElector(consulLeaderCfg, kvPrefix, clusterId, podName)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Error setting up leader election"))
	}

	targetCfg := controller.ConsulTargetConfig{
		ConsulConfig:    consulTargetCfg,
		KvPrefix:        kvPrefix,
		ServicesKeyTmpl: servicesKeyTemplate,
		ClusterId:       clusterId,
		Elector:         elector,
		ServicesEnabled: servicesEnabled,
	}
	target, err := controller.NewConsulTarget(targetCfg)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Error setting up Consul target"))
	}

	sw := controller.NewServiceWatcher(ic,
		namespaces,
		clusterId,
		target,
		queue.NewStatsdQueueMetrics(stats.Client(), "kubernetes.service_handler"),
	)

	nodeConfig := controller.NodeConfiguration{
		MinCount:        minSelectorNodes,
		Selectors:       nodeSelectors,
		MaxRequeueCount: maxRequeueCount,
	}

	nw := controller.NewNodeWatcher(nodeIC, nodeConfig, target)
	httpSrv := server.New(httpIp, httpPort, stopTimeout)

	runStoppers := []RunStopper{elector, sw, nw, httpSrv}

	for _, rs := range runStoppers {
		go func(rs RunStopper) {
			log.Printf("Starting %s...", rs.String())
			if err := rs.Run(); err != nil {
				log.Fatal(errors.Wrapf(err, "Error starting %s", rs.String()))
			}
		}(rs)
	}

	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)
	<-sigC
	log.Println("Shutting down...")
	stats.Client().Gauge("shutdown", 1, []string{}, 1.0)

	go func() {
		defer close(stoppedC)
		for _, rs := range runStoppers {
			rs.Stop()
			log.Printf("Stopped %s.", rs.String())
		}
	}()

	stats.WithTiming("shutdown_time", nil, func() {
		// make sure stops don't take too long
		timer := time.NewTimer(stopTimeout)
		select {
		case <-timer.C:
			log.Println("goroutines took too long to stop. Exiting.")
		case <-stoppedC:
			log.Println("Stopped.")
		}
		os.Stdout.Sync()
	})
}
