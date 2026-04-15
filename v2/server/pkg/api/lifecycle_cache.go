package api

import (
	"context"
	"fmt"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *apiServer) startLifecycleCache(ctx context.Context, restCfg *rest.Config, scheme *runtime.Scheme) error {
	if s == nil || restCfg == nil || scheme == nil {
		return nil
	}

	lifecycleCache, err := ctrlcache.New(restCfg, ctrlcache.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create cache failed: %w", err)
	}

	informer, err := lifecycleCache.GetInformer(ctx, &devboxv1alpha2.Devbox{})
	if err != nil {
		return fmt.Errorf("get devbox informer failed: %w", err)
	}

	if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			s.syncGatewayIndexByObject(obj)
			s.notifyLifecycleRunnerByObject(obj)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			s.replaceGatewayIndexByObjects(oldObj, newObj)
			s.notifyLifecycleRunnerByObject(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			s.deleteGatewayIndexByObject(obj)
			s.notifyLifecycleRunnerByObject(obj)
		},
	}); err != nil {
		return fmt.Errorf("add informer handler failed: %w", err)
	}

	go func() {
		if startErr := lifecycleCache.Start(ctx); startErr != nil && ctx.Err() == nil {
			s.logError("lifecycle cache stopped unexpectedly", startErr)
		}
	}()

	if !lifecycleCache.WaitForCacheSync(ctx) {
		return fmt.Errorf("wait for lifecycle cache sync failed")
	}

	s.lifecycleReader = lifecycleCache
	if err := s.rebuildGatewayIndex(ctx, lifecycleCache); err != nil {
		return fmt.Errorf("rebuild gateway index failed: %w", err)
	}
	s.logInfo("lifecycle cache started")
	return nil
}

func (s *apiServer) notifyLifecycleRunnerByObject(obj interface{}) {
	key, ok := objectKeyFromInformerObj(obj)
	if !ok {
		s.notifyLifecycleRunner()
		return
	}
	s.notifyLifecycleRunnerForKey(key.Namespace, key.Name)
}

func objectKeyFromInformerObj(obj interface{}) (ctrlclient.ObjectKey, bool) {
	switch typed := obj.(type) {
	case ctrlclient.Object:
		key := ctrlclient.ObjectKeyFromObject(typed)
		if key.Namespace == "" || key.Name == "" {
			return ctrlclient.ObjectKey{}, false
		}
		return key, true
	case toolscache.DeletedFinalStateUnknown:
		return objectKeyFromInformerObj(typed.Obj)
	case *toolscache.DeletedFinalStateUnknown:
		if typed == nil {
			return ctrlclient.ObjectKey{}, false
		}
		return objectKeyFromInformerObj(typed.Obj)
	default:
		return ctrlclient.ObjectKey{}, false
	}
}

func (s *apiServer) syncGatewayIndexByObject(obj interface{}) {
	devbox, ok := devboxFromInformerObj(obj)
	if !ok {
		return
	}
	s.syncGatewayIndex(devbox)
}

func (s *apiServer) replaceGatewayIndexByObjects(oldObj interface{}, newObj interface{}) {
	oldDevbox, oldOK := devboxFromInformerObj(oldObj)
	newDevbox, newOK := devboxFromInformerObj(newObj)
	if oldOK {
		s.deleteGatewayIndex(oldDevbox.Namespace, oldDevbox.Name, oldDevbox.Status.Network.UniqueID)
	}
	if newOK {
		s.syncGatewayIndex(newDevbox)
	}
}

func (s *apiServer) deleteGatewayIndexByObject(obj interface{}) {
	devbox, ok := devboxFromInformerObj(obj)
	if !ok {
		return
	}
	s.deleteGatewayIndex(devbox.Namespace, devbox.Name, devbox.Status.Network.UniqueID)
}

func devboxFromInformerObj(obj interface{}) (*devboxv1alpha2.Devbox, bool) {
	switch typed := obj.(type) {
	case *devboxv1alpha2.Devbox:
		if typed == nil || typed.Namespace == "" || typed.Name == "" {
			return nil, false
		}
		return typed, true
	case devboxv1alpha2.Devbox:
		if typed.Namespace == "" || typed.Name == "" {
			return nil, false
		}
		copy := typed
		return &copy, true
	case toolscache.DeletedFinalStateUnknown:
		return devboxFromInformerObj(typed.Obj)
	case *toolscache.DeletedFinalStateUnknown:
		if typed == nil {
			return nil, false
		}
		return devboxFromInformerObj(typed.Obj)
	default:
		return nil, false
	}
}
