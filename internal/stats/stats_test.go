package stats_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"

	statsv1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"

	"github.com/elivlo/pvc-autoscaler/internal/stats"
)

const mi = 1 << 20

var summary = statsv1.Summary{
	Pods: []statsv1.PodStats{
		{
			VolumeStats: []statsv1.VolumeStats{
				{PVCRef: &statsv1.PVCReference{Name: "pvc0", Namespace: "ns0"},
					FsStats: statsv1.FsStats{CapacityBytes: new(uint64(mi * 1000)),
						AvailableBytes: new(uint64(mi * 700)),
						UsedBytes:      new(uint64(mi * 300)),
					},
				},
			},
		},
		{
			VolumeStats: []statsv1.VolumeStats{
				{
					PVCRef: &statsv1.PVCReference{Name: "pvc0", Namespace: "ns1"},
					FsStats: statsv1.FsStats{CapacityBytes: new(uint64(0)),
						AvailableBytes: new(uint64(0)),
					},
				},
				{
					PVCRef: &statsv1.PVCReference{Name: "pvc1", Namespace: "ns1"},
					FsStats: statsv1.FsStats{CapacityBytes: new(uint64(mi * 2000)),
						AvailableBytes: new(uint64(mi * 200)),
						UsedBytes:      new(uint64(mi * 1800)),
					},
				},
			},
		},
	},
}

var _ = Describe("Stats client", func() {
	var srv *httptest.Server
	var client *stats.Client
	BeforeEach(func() {
		var err error
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(BeIdenticalTo("/api/v1/nodes/test-node-1/proxy/stats/summary"))
			enc := json.NewEncoder(w)
			err = enc.Encode(summary)
			Expect(err).NotTo(HaveOccurred())
		}))
		client, err = stats.New(&rest.Config{Host: srv.URL})
		Expect(err).NotTo(HaveOccurred())
	})
	AfterEach(func() {
		srv.Close()
	})

	It("stats are available for pvc in ns0", func() {
		u, err := client.VolumeStats(context.Background(), "test-node-1", "ns0", "pvc0")
		Expect(err).NotTo(HaveOccurred())
		Expect(u.AvailableBytes).To(BeIdenticalTo(int64(mi * 700)))
		Expect(u.UsedBytes).To(BeIdenticalTo(int64(mi * 300)))
		Expect(u.CapacityBytes).To(BeIdenticalTo(int64(mi * 1000)))
		Expect(u.UsedPercent).To(BeIdenticalTo(int64(30)))
	})

	It("stats are available for pvc in ns1", func() {
		u, err := client.VolumeStats(context.Background(), "test-node-1", "ns1", "pvc1")
		Expect(err).NotTo(HaveOccurred())
		Expect(u.AvailableBytes).To(BeIdenticalTo(int64(mi * 200)))
		Expect(u.UsedBytes).To(BeIdenticalTo(int64(mi * 1800)))
		Expect(u.CapacityBytes).To(BeIdenticalTo(int64(mi * 2000)))
		Expect(u.UsedPercent).To(BeIdenticalTo(int64(90)))
	})

	It("stats are incomplete for pvc", func() {
		_, err := client.VolumeStats(context.Background(), "test-node-1", "ns1", "pvc0")
		Expect(err).To(MatchError(stats.ErrIncomplete))
	})

	It("stats are not found for pvc", func() {
		_, err := client.VolumeStats(context.Background(), "test-node-1", "ns1", "pvc2")
		Expect(err).To(MatchError(stats.ErrNotFound))
	})
})
