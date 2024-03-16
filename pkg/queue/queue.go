package queue

// Interface defines the methods expected on a queue implementation
// to be functional with the controllers that are present
type Interface interface {
	Add(v interface{}, priority float64)
	Failed(v interface{})
	Forget(v interface{})
	Get() (interface{}, error)
	Len() int
	NumRequeues(v interface{}) int
	Peek(v interface{}) (*Item, error)
}

// Item is the items we want to place on the queue
// we do not want to expose the properties publically so they
// cannot be altered, the methods attached are read-only for
// peeking into the queue
type Item struct {
	value    interface{}
	priority float64
	index    int
}

// Value returns the value that is present in the item
func (i *Item) Value() interface{} {
	return i.value
}

// Priority returns the priority
func (i *Item) Priority() float64 {
	return i.priority
}

// Index returns the index
func (i *Item) Index() int {
	return i.index
}

// ItemHeap is the implementation that fulfills go's heap interface that allows the priority
// queue to work, the above methods wrap these to wrap and add more functionality
type itemHeap []*Item

func (ih *itemHeap) Len() int {
	return len(*ih)
}

func (ih *itemHeap) Less(i, j int) bool {
	return (*ih)[i].priority < (*ih)[j].priority
}

func (ih *itemHeap) Swap(i, j int) {
	(*ih)[i], (*ih)[j] = (*ih)[j], (*ih)[i]
	(*ih)[i].index = i
	(*ih)[j].index = j
}

func (ih *itemHeap) Push(x interface{}) {
	it := x.(*Item)
	it.index = len(*ih)
	*ih = append(*ih, it)
}

func (ih *itemHeap) Pop() interface{} {
	old := *ih
	item := old[len(old)-1]
	*ih = old[0 : len(old)-1]
	return item
}
