package sizing

import (
	"time"

	"github.com/elivlo/pvc-autoscaler/api/v1alpha1"
	"github.com/elivlo/pvc-autoscaler/internal/stats"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ConditionType = "Resizing"

const gi = 1 << 30

type ResizeReason string

const ResizeFailed ResizeReason = "ResizeFailed"
const withinThreshold ResizeReason = "WithinThreshold"
const cooldownActive ResizeReason = "CooldownActive"
const resizeInProgress ResizeReason = "ResizeInProgress"
const maxSizeReached ResizeReason = "MaxSizeReached"
const resize ResizeReason = "Resize"
const Suspend ResizeReason = "Suspended"

func (r ResizeReason) ResizeNeeded() bool {
	return r == resize
}

func (r ResizeReason) Status() metav1.ConditionStatus {
	switch r {
	case resize, resizeInProgress:
		return metav1.ConditionTrue
	default:
		return metav1.ConditionFalse
	}
}

func (r ResizeReason) Message() string {
	switch r {
	case ResizeFailed:
		return "Resizing the PVC failed"
	case withinThreshold:
		return "no resize of PVC needed"
	case cooldownActive:
		return "Waiting for cooldown"
	case resizeInProgress:
		return "Waiting for the volume to be expanded"
	case maxSizeReached:
		return "PVC is at max size"
	case resize:
		return "Requested resize"
	case Suspend:
		return "Autoscaler is suspended"
	default:
		return "No action"
	}
}

func CheckResize(usage *stats.Usage, autoscaler *v1alpha1.PVCAutoscaler, pvc *corev1.PersistentVolumeClaim, now time.Time) (resource.Quantity, ResizeReason) {
	currentSize := pvc.Spec.Resources.Requests.Storage()

	if currentSize.Cmp(*pvc.Status.Capacity.Storage()) > 0 {
		return *currentSize, resizeInProgress
	}

	if *autoscaler.Spec.ThresholdPercent > usage.UsedPercent {
		return *currentSize, withinThreshold
	}

	lastResizeTime := autoscaler.Status.LastResizeTime
	if lastResizeTime != nil && now.Before(lastResizeTime.Time.Add(autoscaler.Spec.Cooldown.Duration)) {
		return *currentSize, cooldownActive
	}

	if currentSize.Cmp(autoscaler.Spec.MaxSize) >= 0 {
		return *currentSize, maxSizeReached
	}

	newSizeInt := (currentSize.Value() + autoscaler.Spec.IncreaseBy.Value() + gi - 1) / gi * gi
	newSize := resource.NewQuantity(newSizeInt, resource.BinarySI)
	if newSize.Cmp(autoscaler.Spec.MaxSize) == 1 {
		return autoscaler.Spec.MaxSize.DeepCopy(), resize
	}

	return *newSize, resize
}
