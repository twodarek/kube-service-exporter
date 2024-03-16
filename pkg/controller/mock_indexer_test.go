package controller

import (
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

var _ cache.Indexer = mockIndexer{}

type mockIndexer struct {
	services map[string]*v1.Service
}

func (m mockIndexer) Add(obj interface{}) error {
	//TODO implement me
	panic("implement me")
}

func (m mockIndexer) Update(obj interface{}) error {
	//TODO implement me
	panic("implement me")
}

func (m mockIndexer) Delete(obj interface{}) error {
	//TODO implement me
	panic("implement me")
}

func (m mockIndexer) List() []interface{} {
	//TODO implement me
	panic("implement me")
}

func (m mockIndexer) ListKeys() []string {
	//TODO implement me
	panic("implement me")
}

func (m mockIndexer) Get(obj interface{}) (item interface{}, exists bool, err error) {
	//TODO implement me
	panic("implement me")
}

func (m mockIndexer) GetByKey(key string) (item interface{}, exists bool, err error) {
	item, exists = m.services[key]
	return
}

func (m mockIndexer) Replace(i []interface{}, s string) error {
	panic("implement me")
}

func (m mockIndexer) Resync() error {
	panic("implement me")
}

func (m mockIndexer) Index(indexName string, obj interface{}) ([]interface{}, error) {
	panic("implement me")
}

func (m mockIndexer) IndexKeys(indexName, indexedValue string) ([]string, error) {
	panic("implement me")
}

func (m mockIndexer) ListIndexFuncValues(indexName string) []string {
	panic("implement me")
}

func (m mockIndexer) ByIndex(indexName, indexedValue string) ([]interface{}, error) {
	panic("implement me")
}

func (m mockIndexer) GetIndexers() cache.Indexers {
	panic("implement me")
}

func (m mockIndexer) AddIndexers(newIndexers cache.Indexers) error {
	panic("implement me")
}
