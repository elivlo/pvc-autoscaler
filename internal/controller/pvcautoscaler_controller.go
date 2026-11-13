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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"

	storagev1alpha1 "github.com/elivlo/pvc-autoscaler/api/v1alpha1"
	"github.com/elivlo/pvc-autoscaler/internal/stats"
)

const requeueTime = time.Minute * 2
const pvcIndexKey = "spec.volumes.pvc.claimName"
const Mi = 1 << 20

type StatsProvider interface {
	VolumeStats(ctx context.Context, nodeName, namespace, pvc string) (*stats.Usage, error)
}

// PVCAutoscalerReconciler reconciles a PVCAutoscaler object
type PVCAutoscalerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Stats  StatsProvider
}

// +kubebuilder:rbac:groups=storage.elivlo.io,resources=pvcautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.elivlo.io,resources=pvcautoscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=storage.elivlo.io,resources=pvcautoscalers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes/proxy,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PVCAutoscaler object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
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
				ObservedGeneration: base.Generation},
		)
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
				ObservedGeneration: base.Generation},
		)
		return result, nil
	}
	if err != nil {
		meta.SetStatusCondition(&autoscaler.Status.Conditions,
			metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "StatsError",
				Message:            fmt.Sprintf("could not retrieve stats for PVC %s: %s", autoscaler.Spec.PVCName, err),
				ObservedGeneration: base.Generation},
		)
		return ctrl.Result{}, err
	}

	autoscaler.Status.Capacity = resource.NewQuantity(usage.CapacityBytes/Mi*Mi, resource.BinarySI)
	autoscaler.Status.Used = resource.NewQuantity(usage.UsedBytes/Mi*Mi, resource.BinarySI)
	autoscaler.Status.UsedPercent = &usage.UsedPercent

	meta.SetStatusCondition(&autoscaler.Status.Conditions,
		metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "StatsAvailable",
			Message:            "stats of PVC are up to date",
			ObservedGeneration: base.Generation},
	)

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
