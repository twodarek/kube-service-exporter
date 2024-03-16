package util

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

const (
	NodeAnnotationPretendReadyUntil = "kube-service-exporter.github.com/pretend-ready-until"
)

// isReadyAndSchedulable returns true if the node is both marked Ready and schedulable false, otherwise.
func isReadyAndSchedulable(node *v1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}

	condition, ok := GetNodeCondition(node, v1.NodeReady)
	if !ok {
		return false
	}

	if condition.Status == v1.ConditionTrue {
		return true
	}

	return false
}

// ShouldInclude returns true if the node should be included in the list
// of available nodes based on whether the Node is ready OR the grace period is still in effect.
// the second return value is the amount of time of the grace period remaining
// (where the state would need to be checked a final time)
// if the amount of time is zero, then the grace period has passed, never existed or was unparseable.
func ShouldInclude(node *v1.Node) (bool, time.Duration) {
	now := time.Now()
	if val, ok := node.Annotations[NodeAnnotationPretendReadyUntil]; ok {
		pretendReadyUntil, err := time.Parse(time.RFC3339, val)
		if err == nil && now.Before(pretendReadyUntil) {
			return true, pretendReadyUntil.Sub(now)
		}
	}

	return isReadyAndSchedulable(node), 0
}
