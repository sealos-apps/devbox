package api

import (
	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func defaultCreateDevboxSpec(resourceCfg CreateDevboxResourceConfig) devboxv1alpha2.DevboxSpec {
	return devboxv1alpha2.DevboxSpec{
		State: devboxv1alpha2.DevboxStateRunning,
		Resource: corev1.ResourceList{
			corev1.ResourceCPU:              resource.MustParse(resourceCfg.CPU),
			corev1.ResourceMemory:           resource.MustParse(resourceCfg.Memory),
			corev1.ResourceEphemeralStorage: resource.MustParse(resourceCfg.StorageLimit),
		},
		Image:      resourceCfg.Image,
		TemplateID: "aa117587-7c09-4fab-bee4-97b833d55981",
		Config: devboxv1alpha2.Config{
			User:       "devbox",
			WorkingDir: "/home/devbox/workspace",
			ReleaseCommand: []string{
				"/bin/bash",
				"-c",
			},
			Env: []corev1.EnvVar{
				{
					Name:  "DEVBOX_SDK_RUN_AS_ROOT",
					Value: "true",
				},
			},
			ReleaseArgs: []string{
				"/home/devbox/workspace/entrypoint.sh prod",
			},
			Ports: []corev1.ContainerPort{
				{
					Name:          "devbox-ssh-port",
					ContainerPort: 22,
					Protocol:      corev1.ProtocolTCP,
				},
			},
			AppPorts: []corev1.ServicePort{
				{
					Name:       "app-port",
					Port:       8080,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt(8080),
				},
			},
		},
		StorageLimit: resourceCfg.StorageLimit,
		NetworkSpec: devboxv1alpha2.NetworkSpec{
			Type: devboxv1alpha2.NetworkTypeSSHGate,
			ExtraPorts: []corev1.ContainerPort{
				{
					ContainerPort: 8080,
				},
			},
		},
		RuntimeClassName: "devbox-runtime",
		Tolerations: []corev1.Toleration{
			{
				Key:      "devbox.sealos.io/node",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
		Affinity: &corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "devbox.sealos.io/node",
									Operator: corev1.NodeSelectorOpExists,
								},
							},
						},
					},
				},
			},
		},
	}
}
