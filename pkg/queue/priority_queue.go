package queue

import (
	"container/heap"
	"errors"
	"sync"
)

// PriorityQueue represents the queue
type PriorityQueue struct {
	failuresLock sync.Mutex
	failures     map[interface{}]int
	lock         sync.Mutex
	items        *itemHeap
	lookup       map[interface{}]*Item
	metrics      Metrics
}

// NewPriorityQueue initializes an empty priority queue.
func NewPriorityQueue() PriorityQueue {
	return PriorityQueue{
		failures: map[interface{}]int{},
		items:    &itemHeap{},
		lookup:   make(map[interface{}]*Item),
		metrics:  NewNoopMetrics(),
	}
}

// NewPriorityQueueWithMetrics allows the type of metric implementation
// to be pased in to the queue
func NewPriorityQueueWithMetrics(metrics Metrics) PriorityQueue {
	return PriorityQueue{
		failures: map[interface{}]int{},
		items:    &itemHeap{},
		lookup:   make(map[interface{}]*Item),
		metrics:  metrics,
	}
}

// Len returns the number of elements in the queue.
func (p *PriorityQueue) Len() int {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.items.Len()
}

// Add inserts a new element into the queue. No action is performed on duplicate elements.
func (p *PriorityQueue) Add(v interface{}, priority float64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	_, ok := p.lookup[v]
	if ok {
		return
	}

	item := &Item{
		value:    v,
		priority: priority,
	}

	heap.Push(p.items, item)

	p.metrics.add(item, p.items.Len())

	p.lookup[v] = item
}

// Get removes the element with the highest priority from the queue and returns it.
// In case of an empty queue, an error is returned.
func (p *PriorityQueue) Get() (interface{}, error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if len(*p.items) == 0 {
		return nil, errors.New("empty queue")
	}

	item := heap.Pop(p.items).(*Item)

	delete(p.lookup, item.value)

	p.metrics.get(item, p.items.Len())

	return item.value, nil
}

// NumRequeues returns the number of times an item has been pushed through the queue
func (p *PriorityQueue) NumRequeues(v interface{}) int {
	p.failuresLock.Lock()
	defer p.failuresLock.Unlock()

	return p.failures[v]
}

// Forget will remove an item from the failures section and then remove it from the queue
func (p *PriorityQueue) Forget(v interface{}) {
	p.lock.Lock()
	p.failuresLock.Lock()
	defer p.lock.Unlock()
	defer p.failuresLock.Unlock()

	item, ok := p.lookup[v]
	if !ok {
		return
	}

	p.metrics.forget(item)

	delete(p.failures, v)
	delete(p.lookup, v)
	heap.Remove(p.items, item.index)
}

// Failed will document if an item has failed and how many times so it can be used
// in comparisons for attempts to process
func (p *PriorityQueue) Failed(v interface{}) {
	p.failuresLock.Lock()
	defer p.failuresLock.Unlock()

	p.failures[v] = p.failures[v] + 1
}

// Peek will allow you to look at an item in the queue without altering the item
// itself
func (p *PriorityQueue) Peek(v interface{}) (*Item, error) {
	item, ok := p.lookup[v]
	if !ok {
		return nil, errors.New("item cannot be found in queue")
	}

	return item, nil
}
