package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
)

func TestStringInSlice(t *testing.T) {
	strs := []string{"wicked", "cool", "super"}

	for _, str := range strs {
		assert.True(t, StringInSlice(str, strs))
	}
	assert.False(t, StringInSlice("Wicked", strs))
	assert.False(t, StringInSlice("rad", strs))
}

func TestParseNodeSelectors(t *testing.T) {
	_, err := ParseNodeSelectors("")
	assert.Error(t, err)

	_, err = ParseNodeSelectors("[")
	assert.Error(t, err)

	selectors, err := ParseNodeSelectors(`["a=b"]`)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a=b"}, selectors)

	selectors, err = ParseNodeSelectors(`["a=b","c=d"]`)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a=b", "c=d"}, selectors)

	selectors, err = ParseNodeSelectors("a=b")
	assert.NoError(t, err)
	assert.Equal(t, []string{"a=b"}, selectors)
}

func TestGetNodeCondition(t *testing.T) {
	// Test that we can get a condition from a node
	node := v1.Node{
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{Type: "bar", Status: v1.ConditionFalse},
				{Type: v1.NodeReady, Status: v1.ConditionTrue},
			},
		},
	}
	cond, ok := GetNodeCondition(&node, v1.NodeReady)
	assert.True(t, ok)
	assert.Equal(t, v1.ConditionTrue, cond.Status)

	// Test that we can't get a condition from a node that doesn't have it
	_, ok = GetNodeCondition(&node, "foo")
	assert.False(t, ok)
}
