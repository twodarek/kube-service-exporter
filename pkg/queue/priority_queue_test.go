package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPriorityQueue(t *testing.T) {
	t.Run("queue reports length correctly", func(t *testing.T) {
		q := NewPriorityQueue()

		q.Add("something", 0)
		assert.Equal(t, 1, q.Len())

		q.Add("something-else", 0)
		assert.Equal(t, 2, q.Len())

		q.Get()
		assert.Equal(t, 1, q.Len())
	})

	t.Run("queue returns error when empty", func(t *testing.T) {
		q := NewPriorityQueue()

		_, err := q.Get()

		assert.Error(t, err)
		assert.EqualError(t, err, "empty queue")
	})

	t.Run("queue does not accept duplicate items", func(t *testing.T) {
		q := NewPriorityQueue()

		q.Add("identical", 0)
		assert.Equal(t, 1, q.Len())

		q.Add("identical", 1)
		assert.Equal(t, 1, q.Len())
	})

	t.Run("queue returns highest priority item first", func(t *testing.T) {
		q := NewPriorityQueue()

		q.Add("low-priority", 3)
		q.Add("medium-priority", 2)
		q.Add("high-priority", 0)

		item, err := q.Get()
		if err != nil {
			assert.FailNow(t, "retrieve from queue errored")
		}

		assert.Equal(t, "high-priority", item)
	})

	t.Run("queue fails item and increases counter, requeues are calculated", func(t *testing.T) {
		q := NewPriorityQueue()

		// add to queue and retrieve
		q.Add("failure", 0)
		item, err := q.Get()
		if err != nil {
			assert.FailNow(t, "retrieve from queue errored")
		}

		// ensure queue is empty
		assert.Equal(t, 0, q.Len())

		// fail item
		q.Failed(item)
		assert.Equal(t, 1, q.failures[item])
		assert.Equal(t, 1, q.NumRequeues(item))
	})

	t.Run("queue forgets item and it is removed from heap and queue", func(t *testing.T) {
		q := NewPriorityQueue()

		q.Add("forget-me-not", 0)
		assert.Equal(t, 1, q.Len())
		assert.NotEmpty(t, q.lookup)

		q.Forget("forget-me-not")
		assert.Equal(t, 0, q.Len())
		assert.Empty(t, q.lookup)
		assert.Empty(t, q.failures)
	})

	t.Run("queue returns silently when forgetting even if item doesn't exist", func(t *testing.T) {
		q := NewPriorityQueue()

		q.Forget("never-forgotten")
		assert.Equal(t, 0, q.Len())
		assert.Empty(t, q.lookup)
		assert.Empty(t, q.failures)
	})

	t.Run("queue can peek at an item without pulling off itemHeap", func(t *testing.T) {
		q := NewPriorityQueue()

		// add to queue
		q.Add("peekaboo", 0)

		// peek into queue
		item, err := q.Peek("peekaboo")
		if err != nil {
			assert.FailNow(t, err.Error())
		}

		assert.Equal(t, float64(0), item.Priority())
		assert.Equal(t, "peekaboo", item.Value())
	})
}
