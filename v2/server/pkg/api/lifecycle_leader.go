package api

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	lifecycleLeaderElectionLockName = "devbox-api-lifecycle"
	lifecycleLeaderElectionName     = "devbox-api-lifecycle"
)

func (s *apiServer) runLifecycleLeaderElector(ctx context.Context) {
	namespace := detectPodNamespace()
	identity := detectLeaderIdentity()
	s.logInfo("lifecycle leader election starting", "namespace", namespace, "identity", identity)

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      lifecycleLeaderElectionLockName,
			Namespace: namespace,
		},
		Client: s.kubeClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identity,
		},
	}

	lec := leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadCtx context.Context) {
				s.logInfo("lifecycle leadership acquired", "identity", identity)
				s.runLifecycleRunner(leadCtx)
			},
			OnStoppedLeading: func() {
				s.logWarn("lifecycle leadership lost", "identity", identity)
			},
			OnNewLeader: func(currentLeader string) {
				if strings.TrimSpace(currentLeader) == identity {
					return
				}
				s.logInfo("lifecycle new leader observed", "leader", currentLeader)
			},
		},
		ReleaseOnCancel: true,
		Name:            lifecycleLeaderElectionName,
	}

	for {
		if ctx.Err() != nil {
			return
		}
		leaderelection.RunOrDie(ctx, lec)
		if ctx.Err() != nil {
			return
		}
		s.logInfo("lifecycle leader election loop restarting")
		time.Sleep(time.Second)
	}
}

func detectPodNamespace() string {
	if env := strings.TrimSpace(os.Getenv("POD_NAMESPACE")); env != "" {
		return env
	}
	content, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil {
		if ns := strings.TrimSpace(string(content)); ns != "" {
			return ns
		}
	}
	return "default"
}

func detectLeaderIdentity() string {
	if env := strings.TrimSpace(os.Getenv("POD_NAME")); env != "" {
		return env
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return fmt.Sprintf("devbox-api-%d", time.Now().UnixNano())
}
