package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"reflect"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"

	"github.com/github/kube-service-exporter/pkg/consul"
	"github.com/github/kube-service-exporter/pkg/leader"
	capi "github.com/hashicorp/consul/api"
)

type ConsulTarget struct {
	client          *capi.Client
	elector         leader.LeaderElector
	hostIP          string
	kvPrefix        string
	clusterId       string
	servicesEnabled bool
	kv              *consul.InstrumentedKV
	agent           *consul.InstrumentedAgent
	servicesKeyTmpl string
}

type ExportedNode struct {
	Name    string
	Address string
}

type ConsulTargetConfig struct {
	ConsulConfig *capi.Config
	KvPrefix     string

	// ServicesKeyTmpl is the go template used for each service. Defaults to
	// services/{{ .Id() }}
	// Can be used to namespace keys for better lookup efficiency, e.g.
	// services/{{ .LoadBalancerClass }}/{{ .Id() }}
	ServicesKeyTmpl string
	ClusterId       string
	Elector         leader.LeaderElector
	// ServicesEnabled defines whether or not to store services as Consul Services
	// in addition to in KV metadata. This option requires kube-service-exporter
	// to be deployed as a DaemonSet
	ServicesEnabled bool
}

type exportedNodeList []ExportedNode

var _ ExportTarget = (*ConsulTarget)(nil)

func NewConsulTarget(cfg ConsulTargetConfig) (*ConsulTarget, error) {
	hostIP, _, err := net.SplitHostPort(cfg.ConsulConfig.Address)
	if err != nil {
		return nil, err
	}

	client, err := capi.NewClient(cfg.ConsulConfig)
	if err != nil {
		return nil, err
	}

	return &ConsulTarget{
		client:          client,
		elector:         cfg.Elector,
		hostIP:          hostIP,
		clusterId:       cfg.ClusterId,
		kvPrefix:        cfg.KvPrefix,
		servicesEnabled: cfg.ServicesEnabled,
		kv:              consul.NewInstrumentedKV(client),
		agent:           consul.NewInstrumentedAgent(client),
		servicesKeyTmpl: cfg.ServicesKeyTmpl,
	}, nil
}

func (t *ConsulTarget) Create(es *ExportedService) (bool, error) {
	asr := t.asrFromExportedService(es)

	wait := 15 * time.Second
	if err := t.elector.WaitForLeader(wait, time.Second); err != nil {
		return false, errors.Wrapf(err, "No leader found after %s, refusing to create %s", wait, es.Id())
	}

	updateService, err := t.shouldUpdateService(asr)
	if err != nil {
		return false, errors.Wrap(err, "Error calling shouldUpdateService")
	}

	if updateService {
		log.Printf("Updating Consul Service %s due to registration change", asr.ID)
		if err := t.agent.ServiceRegister(asr); err != nil {
			return false, err
		}
	}

	if t.elector.IsLeader() {
		updateKV, err := t.shouldUpdateKV(es)
		if err != nil {
			return false, errors.Wrap(err, "Error calling shouldUpdateKV")
		}

		if !updateKV {
			return true, nil
		}

		log.Printf("[LEADER] Writing KV metadata for %s", es.Id())
		if err := t.writeKV(es); err != nil {
			return false, err
		}
	}

	return true, nil
}

// Update will update the representation of the ExportedService in Consul,
// cleaning up old keys if the old key differs from the new key.
// returns true when the key was created, false otherwise
func (t *ConsulTarget) Update(old *ExportedService, new *ExportedService) (bool, error) {
	created, err := t.Create(new)
	if err != nil {
		return false, errors.Wrap(err, "error creating new key")
	}

	if _, err := t.clean(old, new); err != nil {
		return created, errors.Wrap(err, "error deleting old/changed key")
	}

	return created, err
}

// clean will clean up the old key if the key value changed as part of an update
// returns true if the old key was removed, false otherwise
func (t *ConsulTarget) clean(old *ExportedService, new *ExportedService) (bool, error) {
	if old == nil {
		return false, nil
	}

	// ignore metadataPrefix errors here because:
	// * we don't want to output an error because not being able to calculate
	//   the _old_ prefix
	// * the new prefix was _just_ calculated up in Create above, so it's
	//   redundant
	oldPrefix, _ := t.metadataPrefix(old)
	newPrefix, _ := t.metadataPrefix(new)

	if oldPrefix == newPrefix {
		return false, nil
	}

	return t.Delete(old)
}

func (t *ConsulTarget) Delete(es *ExportedService) (bool, error) {
	if err := t.agent.ServiceDeregister(es.Id()); err != nil {
		return false, err
	}

	if t.elector.IsLeader() {
		log.Printf("[LEADER] Deleting KV metadata for %s", es.Id())
		prefix, err := t.metadataPrefix(es)
		if err != nil {
			return false, err
		}

		if _, err := t.kv.DeleteTree(prefix, &capi.WriteOptions{}, []string{"kv:metadata"}); err != nil {
			return false, err
		}
	}

	return true, nil
}

func (t *ConsulTarget) metadataPrefix(es *ExportedService) (string, error) {
	if t.servicesKeyTmpl != "" {
		funcMap := template.FuncMap{"id": es.Id}

		tmpl, err := template.New("metadata-prefix").Funcs(funcMap).Parse(t.servicesKeyTmpl)
		if err != nil {
			return "", err
		}

		var builder strings.Builder
		if err := tmpl.Execute(&builder, es); err != nil {
			return "", err
		}

		return fmt.Sprintf("%s/services/%s/clusters/%s", t.kvPrefix, builder.String(), es.ClusterId), nil
	}
	return fmt.Sprintf("%s/services/%s/clusters/%s", t.kvPrefix, es.Id(), es.ClusterId), nil
}

// Write out metadata to where it belongs in Consul using a transaction.  This
// should only ever get called by the current leader
func (t *ConsulTarget) writeKV(es *ExportedService) error {
	esJson, err := json.Marshal(es)
	if err != nil {
		return errors.Wrap(err, "Error marshaling ExportedService JSON")
	}

	key, err := t.metadataPrefix(es)
	if err != nil {
		return errors.Wrap(err, "error calculating key prefix")
	}

	kvPair := capi.KVPair{
		Key:   key,
		Value: esJson,
	}

	_, err = t.kv.Put(&kvPair, nil, []string{"kv:metadata"})

	return err
}

func (t *ConsulTarget) asrFromExportedService(es *ExportedService) *capi.AgentServiceRegistration {
	return &capi.AgentServiceRegistration{
		ID:      es.Id(),
		Name:    es.Id(),
		Tags:    []string{es.ClusterId, "kube-service-exporter"},
		Port:    int(es.Port),
		Address: t.hostIP,
		Check: &capi.AgentServiceCheck{
			Name:     "NodePort",
			TCP:      fmt.Sprintf("%s:%d", t.hostIP, es.Port),
			Interval: "10s",
		},
	}
}

// shouldUpdateKV checks if the hash of the ExportedService is the same as the
// hash stored in Consul.  If they differ, KV data is updated.
func (t *ConsulTarget) shouldUpdateKV(es *ExportedService) (bool, error) {
	key, err := t.metadataPrefix(es)
	if err != nil {
		return false, errors.Wrap(err, "error calculating key prefix")
	}

	qo := capi.QueryOptions{RequireConsistent: true}

	kvPair, _, err := t.kv.Get(key, &qo, []string{"kv:metadata"})
	if err != nil {
		return true, errors.Wrap(err, "Error getting KV hash")
	}

	if kvPair == nil {
		return true, nil
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(kvPair.Value, &meta); err != nil {
		return true, errors.Wrap(err, "Error unmarshaling JSON from Consul")
	}

	consulHash, ok := meta["hash"]
	if !ok {
		return true, nil
	}

	hash, err := es.Hash()
	if err != nil {
		return true, errors.Wrap(err, "Error getting ExportedService Hash in shouldUpdateKV")
	}

	if consulHash == hash {
		return false, nil
	}

	return true, nil
}

// returns true if the active AgentService in Consul is equivalent to the
// AgentServiceRegistration passed in.
func (t *ConsulTarget) shouldUpdateService(asr *capi.AgentServiceRegistration) (bool, error) {
	if !t.servicesEnabled {
		return false, nil
	}

	services, err := t.agent.Services()
	if err != nil {
		return false, errors.Wrap(err, "Error getting agent services")
	}

	// Consul Service doesn't exist
	service, found := services[asr.ID]
	if !found {
		return true, nil
	}

	sort.Strings(asr.Tags)
	sort.Strings(service.Tags)

	// verify that the AgentService and AgentServiceRegistration are the same.
	// Because there is no API for it, this does not (and cannot) verify if the
	// Consul Agent Service *Check* has changed, but since the Check is
	// generated from metadata present in the AgentService, this should be fine.
	if asr.ID == service.ID &&
		asr.Name == service.Service &&
		reflect.DeepEqual(asr.Tags, service.Tags) &&
		asr.Port == service.Port &&
		asr.Address == service.Address {
		return false, nil
	}

	return true, nil
}

func (t *ConsulTarget) WriteNodes(nodes []*v1.Node) error {
	var exportedNodes exportedNodeList
	tags := []string{"kv:nodes"}

	if !t.elector.IsLeader() {
		// do nothing
		return nil
	}

	for _, k8sNode := range nodes {
		for _, addr := range k8sNode.Status.Addresses {
			if addr.Type != v1.NodeInternalIP {
				continue
			}

			exportedNode := ExportedNode{
				Name:    k8sNode.Name,
				Address: addr.Address,
			}
			exportedNodes = append(exportedNodes, exportedNode)
		}
	}

	sort.Sort(exportedNodes)

	nodeJson, err := json.Marshal(exportedNodes)
	if err != nil {
		return errors.Wrap(err, "Error marshaling node JSON")
	}

	key := fmt.Sprintf("%s/nodes/%s", t.kvPrefix, t.clusterId)

	current, _, err := t.kv.Get(key, &capi.QueryOptions{}, tags)
	if err != nil {
		return errors.Wrapf(err, "Error getting %s key", key)
	}

	if current != nil && bytes.Equal(current.Value, nodeJson) {
		// nothing changed
		return nil
	}

	kv := capi.KVPair{
		Key:   key,
		Value: nodeJson,
	}

	if _, err := t.kv.Put(&kv, nil, tags); err != nil {
		return errors.Wrapf(err, "Error writing %s key", key)
	}

	log.Println("[LEADER] Writing Node list to ", key)
	return nil
}

func (esl exportedNodeList) Len() int {
	return len(esl)
}

func (esl exportedNodeList) Swap(i, j int) {
	esl[i], esl[j] = esl[j], esl[i]
}

func (esl exportedNodeList) Less(i, j int) bool {
	return esl[i].Name < esl[j].Name
}
