package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	statsv1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

var ErrNotFound = errors.New("volume stats not found")
var ErrIncomplete = errors.New("volume stats are incomplete")

type Usage struct {
	UsedBytes      int64
	UsedPercent    int64
	CapacityBytes  int64
	AvailableBytes int64
	Time           time.Time
}

type Client struct {
	clientset *kubernetes.Clientset
}

func New(config *rest.Config) (*Client, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &Client{clientset: clientset}, nil
}

func (c *Client) VolumeStats(ctx context.Context, nodeName, namespace, pvc string) (*Usage, error) {
	resp, err := c.clientset.CoreV1().RESTClient().Get().Resource("nodes").Name(nodeName).SubResource("proxy").Suffix("stats", "summary").DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("stats/summary from node %s: %w", nodeName, err)
	}

	summary := &statsv1.Summary{}
	err = json.Unmarshal(resp, summary)
	if err != nil {
		return nil, fmt.Errorf("stats/summary from node %s: %w", nodeName, err)
	}

	var stats *statsv1.VolumeStats
	for _, p := range summary.Pods {
		vIdx := slices.IndexFunc(p.VolumeStats, func(v statsv1.VolumeStats) bool {
			return v.PVCRef != nil && v.PVCRef.Namespace == namespace && v.PVCRef.Name == pvc
		})
		if vIdx > -1 {
			stats = &p.VolumeStats[vIdx]
			break
		}
	}

	if stats == nil {
		return nil, ErrNotFound
	}

	if stats.CapacityBytes == nil || *stats.CapacityBytes == 0 || stats.AvailableBytes == nil || stats.UsedBytes == nil {
		return nil, ErrIncomplete
	}

	return &Usage{
		UsedBytes:      int64(*stats.UsedBytes),
		UsedPercent:    100 * int64(*stats.UsedBytes) / int64(*stats.CapacityBytes),
		CapacityBytes:  int64(*stats.CapacityBytes),
		AvailableBytes: int64(*stats.AvailableBytes),
		Time:           stats.Time.Time,
	}, nil
}
