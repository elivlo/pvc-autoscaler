package sizing_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/elivlo/pvc-autoscaler/api/v1alpha1"
	"github.com/elivlo/pvc-autoscaler/internal/stats"

	"github.com/elivlo/pvc-autoscaler/internal/sizing"
)

var _ = Describe("Sizing", func() {
	DescribeTable("ResizeNeeded",
		func(reason sizing.ResizeReason, want bool) {
			Expect(reason.ResizeNeeded()).To(Equal(want))
		},
		Entry("exceeded threshold", sizing.ResizeReason("ThresholdExceeded"), true),
		Entry("resize in progress", sizing.ResizeReason("ResizeInProgress"), false),
		Entry("withing threshold", sizing.ResizeReason("WithinThreshold"), false),
		Entry("already at max size", sizing.ResizeReason("MaxSizeReached"), false),
		Entry("in cooldown", sizing.ResizeReason("CooldownActive"), false),
	)

	DescribeTable("Status",
		func(reason sizing.ResizeReason, want string) {
			Expect(reason.Status()).To(Equal(metav1.ConditionStatus(want)))
		},
		Entry("exceeded threshold", sizing.ResizeReason("ThresholdExceeded"), "True"),
		Entry("resize in progress", sizing.ResizeReason("ResizeInProgress"), "True"),
		Entry("withing threshold", sizing.ResizeReason("WithinThreshold"), "False"),
		Entry("already at max size", sizing.ResizeReason("MaxSizeReached"), "False"),
		Entry("in cooldown", sizing.ResizeReason("CooldownActive"), "False"),
	)
})

var _ = Describe("Sizing checks", func() {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var (
		usage      *stats.Usage
		autoscaler *v1alpha1.PVCAutoscaler
		pvc        *corev1.PersistentVolumeClaim
	)
	BeforeEach(func() {
		usage = &stats.Usage{UsedPercent: 90}
		autoscaler = &v1alpha1.PVCAutoscaler{
			Spec: v1alpha1.PVCAutoscalerSpec{
				ThresholdPercent: new(int64(80)),
				Cooldown:         &metav1.Duration{Duration: time.Hour},
				MaxSize:          resource.MustParse("10Gi"),
				IncreaseBy:       resource.MustParse("1Gi"),
			},
		}
		pvc = &corev1.PersistentVolumeClaim{
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			},
			Status: corev1.PersistentVolumeClaimStatus{
				Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		}
	})

	It("returns threshold is exceeded", func() {
		size, reason := sizing.CheckResize(usage, autoscaler, pvc, now)
		Expect(reason).To(Equal(sizing.ResizeReason("ThresholdExceeded")))
		Expect(size.String()).To(Equal("2Gi"))
	})

	Context("when a resize is already in progress", func() {
		BeforeEach(func() {
			pvc.Status.Capacity[corev1.ResourceStorage] = resource.MustParse("500Mi")
		})
		It("returns resize in progress", func() {
			size, reason := sizing.CheckResize(usage, autoscaler, pvc, now)
			Expect(reason).To(Equal(sizing.ResizeReason("ResizeInProgress")))
			Expect(size.String()).To(Equal("1Gi"))
		})
	})

	Context("when usage is below the threshold", func() {
		BeforeEach(func() {
			usage.UsedPercent = 50
		})
		It("returns in threshold", func() {
			_, reason := sizing.CheckResize(usage, autoscaler, pvc, now)
			Expect(reason).To(Equal(sizing.ResizeReason("WithinThreshold")))
		})
	})

	Context("when the cooldown is active", func() {
		BeforeEach(func() {
			autoscaler.Status = v1alpha1.PVCAutoscalerStatus{LastResizeTime: &metav1.Time{Time: now.Add(time.Minute)}}
		})
		It("returns in cooldown", func() {
			_, reason := sizing.CheckResize(usage, autoscaler, pvc, now)
			Expect(reason).To(Equal(sizing.ResizeReason("CooldownActive")))
		})
	})

	Context("when already at max size", func() {
		BeforeEach(func() {
			pvc.Status.Capacity[corev1.ResourceStorage] = resource.MustParse("10Gi")
			pvc.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("10Gi")
		})
		It("returns max size reached", func() {
			size, reason := sizing.CheckResize(usage, autoscaler, pvc, now)
			Expect(reason).To(Equal(sizing.ResizeReason("MaxSizeReached")))
			Expect(size.String()).To(Equal("10Gi"))
		})
	})

	Context("when growth would exceed max size", func() {
		BeforeEach(func() {
			autoscaler.Spec.IncreaseBy = resource.MustParse("20Gi")
		})
		It("returns max size of autoscaler", func() {
			size, reason := sizing.CheckResize(usage, autoscaler, pvc, now)
			Expect(reason).To(Equal(sizing.ResizeReason("ThresholdExceeded")))
			Expect(size.String()).To(Equal("10Gi"))
		})
	})
})
