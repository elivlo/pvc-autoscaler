package main

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	statsv1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

const (
	Mi = 1 << 20 // 1,048,576
	Gi = 1 << 30 // 1,073,741,824
)

func test() {
	cfg, err := clientcmd.BuildConfigFromFlags("", "kubeconfig") //clientcmd.RecommendedHomeFile)
	if err != nil {
		panic(err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()

	nodes, err := client.CoreV1().Nodes().List(ctx, v1.ListOptions{})
	if err != nil {
		panic(err)
	}

	for _, node := range nodes.Items {
		resp, err := client.CoreV1().RESTClient().Get().Resource("nodes").Name(node.Name).SubResource("proxy").Suffix("stats", "summary").DoRaw(ctx)
		if err != nil {
			panic(err)
		}

		summary := &statsv1.Summary{}
		err = json.Unmarshal(resp, summary)
		if err != nil {
			panic(err)
		}

		for _, pod := range summary.Pods {
			for _, volume := range pod.VolumeStats {
				if volume.PVCRef == nil {
					fmt.Printf("Volume %s has no PVC\n", volume.Name)
					continue
				}

				if volume.CapacityBytes == nil || volume.AvailableBytes == nil {
					fmt.Printf("Volume %s has no valid capacity\n", volume.Name)
					continue
				}

				fmt.Printf("volume %s: used=%.2fGi capacity=%.2fGi percent=%.2f%%\n", volume.Name,
					float64(*volume.UsedBytes)/Gi, float64(*volume.CapacityBytes)/Gi, float64(*volume.UsedBytes)/float64(*volume.CapacityBytes)*100)
			}
		}
	}

	_ = statsv1.Summary{}

}
