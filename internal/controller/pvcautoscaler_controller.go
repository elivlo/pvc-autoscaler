/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"

	storagev1alpha1 "github.com/elivlo/pvc-autoscaler/api/v1alpha1"
	"github.com/elivlo/pvc-autoscaler/internal/sizing"
	"github.com/elivlo/pvc-autoscaler/internal/stats"
)

const requeueTime = time.Minute * 2
const pvcIndexKey = "spec.volumes.pvc.claimName"
const mi = 1 << 20

type StatsProvider interface {
	VolumeStats(ctx context.Context, nodeName, namespace, pvc string) (*stats.Usage, error)
}

// PVCAutoscalerReconciler reconciles a PVCAutoscaler object
type PVCAutoscalerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder recorder.EventRecorder
	Stats    StatsProvider
}

// +kubebuilder:rbac:groups=storage.elivlo.io,resources=pvcautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.elivlo.io,resources=pvcautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=storage.elivlo.io,resources=pvcautoscalers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=nodes/proxy,verbs=get
// +kubebuilder:rbac:groups="storage.k8s.io",resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups="events.k8s.io",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/reconcile
func (r *PVCAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	l := logf.FromContext(ctx)
	l.Info("Reconciling")

	var autoscaler storagev1alpha1.PVCAutoscaler
	if err := r.Get(ctx, req.NamespacedName, &autoscaler); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	base := autoscaler.DeepCopy()

	defer func() {
		autoscaler.Status.ObservedGeneration = autoscaler.Generation
		if equality.Semantic.DeepEqual(base.Status, autoscaler.Status) {
			return
		}
		if pErr := r.Status().Patch(ctx, &autoscaler, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); client.IgnoreNotFound(pErr) != nil {
			err = errors.Join(err, pErr)
		}
		if err == nil {
			l.Info("Updated PVCAutoscaler status")
		}
	}()

	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(req.Namespace), client.MatchingFields{pvcIndexKey: autoscaler.Spec.PVCName}); err != nil {
		return ctrl.Result{}, err
	}

	result = ctrl.Result{
		RequeueAfter: requeueTime + time.Duration(rand.Int63n(int64(time.Second*10))), // jitter between 0-10 seconds
	}

	podIdx := slices.IndexFunc(pods.Items, func(p corev1.Pod) bool {
		return p.Status.Phase == corev1.PodRunning && p.Spec.NodeName != ""
	})
	if podIdx == -1 {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "StatsUnavailable",
				Message:            "no running pod mounts PVC " + autoscaler.Spec.PVCName,
				ObservedGeneration: base.Generation,
			})
		return result, nil
	}

	usage, err := r.Stats.VolumeStats(ctx, pods.Items[podIdx].Spec.NodeName, req.Namespace, autoscaler.Spec.PVCName)
	if errors.Is(err, stats.ErrNotFound) || errors.Is(err, stats.ErrIncomplete) {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "StatsUnavailable",
				Message:            "no stats available for PVC " + autoscaler.Spec.PVCName,
				ObservedGeneration: base.Generation,
			})
		return result, nil
	}
	if err != nil {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "StatsError",
				Message:            fmt.Sprintf("could not retrieve stats for PVC %s: %s", autoscaler.Spec.PVCName, err),
				ObservedGeneration: base.Generation,
			})
		return ctrl.Result{}, err
	}

	autoscaler.Status.Capacity = resource.NewQuantity(usage.CapacityBytes/mi*mi, resource.BinarySI)
	autoscaler.Status.Used = resource.NewQuantity(usage.UsedBytes/mi*mi, resource.BinarySI)
	autoscaler.Status.UsedPercent = &usage.UsedPercent

	meta.SetStatusCondition(&autoscaler.Status.Conditions,
		metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "StatsAvailable",
			Message:            "stats of PVC are up to date",
			ObservedGeneration: base.Generation,
		})

	var pvc corev1.PersistentVolumeClaim
	if err = r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: autoscaler.Spec.PVCName}, &pvc); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&autoscaler.Status.Conditions,
				metav1.Condition{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					Reason:             "PVCNotFound",
					Message:            "could not find PVC",
					ObservedGeneration: base.Generation,
				})
		}
		return result, client.IgnoreNotFound(err)
	}
	pvcBase := pvc.DeepCopy()

	if pvc.Status.Phase != corev1.ClaimBound {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "PVCNotBound",
				Message:            "the PVC is currently not bound",
				ObservedGeneration: base.Generation,
			})
		return result, nil
	}

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName == "" {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "ExpansionNotAllowed",
				Message:            "no valid StorageClass set on PVC",
				ObservedGeneration: base.Generation,
			})
		return result, nil
	}

	var sc storagev1.StorageClass
	if err = r.Get(ctx, client.ObjectKey{Name: *pvc.Spec.StorageClassName}, &sc); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&autoscaler.Status.Conditions,
				metav1.Condition{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					Reason:             "StorageClassNotFound",
					Message:            "could not find StorageClass",
					ObservedGeneration: base.Generation,
				})
		}
		return result, client.IgnoreNotFound(err)
	}

	if sc.AllowVolumeExpansion == nil || !*sc.AllowVolumeExpansion {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "ExpansionNotAllowed",
				Message:            "the StorageClass does not allow resizing the volume",
				ObservedGeneration: base.Generation,
			})
		return result, nil
	}

	if autoscaler.Spec.Suspend {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               sizing.ConditionType,
				Status:             sizing.Suspend.Status(),
				Reason:             string(sizing.Suspend),
				Message:            sizing.Suspend.Message(),
				ObservedGeneration: base.Generation,
			})
		return ctrl.Result{}, nil
	}

	now := time.Now()
	newSize, reason := sizing.CheckResize(usage, &autoscaler, &pvc, now)
	meta.SetStatusCondition(&autoscaler.Status.Conditions,
		metav1.Condition{
			Type:               sizing.ConditionType,
			Status:             reason.Status(),
			Reason:             string(reason),
			Message:            reason.Message(),
			ObservedGeneration: base.Generation,
		})

	if reason.ResizeNeeded() {
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = newSize
		if err = r.Patch(ctx, &pvc, client.MergeFrom(pvcBase)); err != nil {
			meta.SetStatusCondition(&autoscaler.Status.Conditions,
				metav1.Condition{
					Type:               sizing.ConditionType,
					Status:             sizing.ResizeFailed.Status(),
					Reason:             string(sizing.ResizeFailed),
					Message:            sizing.ResizeFailed.Message(),
					ObservedGeneration: base.Generation,
				})
			r.Recorder.Eventf(
				&autoscaler,
				&pvc,
				corev1.EventTypeWarning,
				string(sizing.ResizeFailed),
				"ResizePVC",
				"Resizing of PVC %s from %s to %s failed: %v",
				pvc.Name,
				pvcBase.Spec.Resources.Requests.Storage().String(),
				newSize.String(),
				err,
			)
			return result, client.IgnoreNotFound(err)
		}

		now := metav1.NewTime(now)
		autoscaler.Status.LastResizeTime = &now
		r.Recorder.Eventf(
			&autoscaler,
			&pvc,
			corev1.EventTypeNormal,
			string(reason),
			"ResizePVC",
			"Resized PVC %s from %s to %s",
			pvc.Name,
			pvcBase.Spec.Resources.Requests.Storage().String(),
			newSize.String(),
		)
	}

	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PVCAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(context.TODO(), &corev1.Pod{}, pvcIndexKey, indexVolumeClaim)
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.PVCAutoscaler{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("pvcautoscaler").
		Complete(r)
}

func indexVolumeClaim(obj client.Object) (claimNames []string) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			claimNames = append(claimNames, volume.PersistentVolumeClaim.ClaimName)
		}
	}
	return
}
