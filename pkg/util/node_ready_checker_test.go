package util

import (
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

// testingNode returns a node that is ready and schedulable
func testingNode() v1.Node {
	return v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "a.example.net",
			Annotations: map[string]string{},
		},
		Spec: v1.NodeSpec{
			Unschedulable: false,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:   v1.NodeReady,
					Status: v1.ConditionTrue,
				},
			},
		},
	}
}

func TestIsReady(t *testing.T) {
	t.Run("when node is ready", func(t *testing.T) {
		readyNode := testingNode()
		assert.True(t, isReadyAndSchedulable(&readyNode))

	})

	t.Run("when node is not ready", func(t *testing.T) {
		notReadyNodes := []v1.Node{
			testingNode(),
			testingNode(),
		}

		notReadyNodes[0].Spec.Unschedulable = true
		notReadyNodes[1].Status.Conditions = []v1.NodeCondition{
			{Type: v1.NodeReady, Status: v1.ConditionFalse},
		}

		for _, node := range notReadyNodes {
			assert.False(t, isReadyAndSchedulable(&node))
		}
	})
}

func TestShouldInclude(t *testing.T) {
	t.Run("with no grace period", func(t *testing.T) {
		t.Run("when node is ready", func(t *testing.T) {
			readyNode := testingNode()
			include, _ := ShouldInclude(&readyNode)
			assert.True(t, include, "node should be included")
		})

		t.Run("when node is not ready", func(t *testing.T) {
			notReadyNodes := []v1.Node{
				testingNode(),
				testingNode(),
			}

			notReadyNodes[0].Spec.Unschedulable = true
			notReadyNodes[1].Status.Conditions = []v1.NodeCondition{
				{Type: v1.NodeReady, Status: v1.ConditionFalse},
			}

			for _, node := range notReadyNodes {
				include, _ := ShouldInclude(&node)
				assert.False(t, include, "node should not be included")
			}
		})
	})

	t.Run("within grace period", func(t *testing.T) {
		t.Run("when node is ready", func(t *testing.T) {
			node := testingNode()
			node.Annotations[NodeAnnotationPretendReadyUntil] = time.Now().Add(5 * time.Minute).Format(time.RFC3339)

			include, timeLeft := ShouldInclude(&node)
			assert.NotEqual(t, time.Duration(0), timeLeft, "remaining time should not be zero")
			assert.True(t, include, "node should be included")
		})

		t.Run("when node is no longer ready", func(t *testing.T) {
			node := testingNode()
			node.Annotations[NodeAnnotationPretendReadyUntil] = time.Now().Add(5 * time.Minute).Format(time.RFC3339)

			node.Spec.Unschedulable = true

			include, timeLeft := ShouldInclude(&node)
			assert.NotEqual(t, time.Duration(0), timeLeft, "remaining time should not be zero")
			assert.True(t, include, "node should be included")
		})
	})
	t.Run("after grace period", func(t *testing.T) {
		t.Run("when node is ready", func(t *testing.T) {
			node := testingNode()
			node.Annotations[NodeAnnotationPretendReadyUntil] = time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
			include, timeLeft := ShouldInclude(&node)
			assert.Equal(t, time.Duration(0), timeLeft, "remaining time should not be zero")
			assert.True(t, include, "node should be included")
		})

		t.Run("when node is not ready", func(t *testing.T) {
			node := testingNode()
			node.Annotations[NodeAnnotationPretendReadyUntil] = time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
			node.Spec.Unschedulable = true

			include, timeLeft := ShouldInclude(&node)
			assert.Equal(t, time.Duration(0), timeLeft, "remaining time should not be zero")
			assert.False(t, include, "node should not be included")
		})
	})

}
