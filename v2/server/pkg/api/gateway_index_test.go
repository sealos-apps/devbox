package api

import (
	"testing"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
)

func TestGatewayIndexTracksLatestUniqueID(t *testing.T) {
	srv := &apiServer{
		cfg: ServerConfig{
			Gateway: GatewayConfig{
				Domain:     "devbox-gateway.staging-usw-1.sealos.io",
				PathPrefix: "/codex",
				Port:       1317,
			},
		},
	}

	devbox := &devboxv1alpha2.Devbox{}
	devbox.Namespace = "ns-test"
	devbox.Name = "demo-devbox"
	devbox.Status.Network.UniqueID = "piano-stay-ndor"

	srv.syncGatewayIndex(devbox)

	entry, ok := srv.getGatewayIndex("piano-stay-ndor")
	if !ok {
		t.Fatalf("expected gateway index entry")
	}
	if entry.URL != "https://devbox-gateway.staging-usw-1.sealos.io/codex/piano-stay-ndor" {
		t.Fatalf("unexpected gateway url: %s", entry.URL)
	}

	devbox.Status.Network.UniqueID = "new-unique-id"
	srv.syncGatewayIndex(devbox)

	if _, ok := srv.getGatewayIndex("piano-stay-ndor"); ok {
		t.Fatalf("expected previous uniqueID to be removed")
	}
	if _, ok := srv.getGatewayIndex("new-unique-id"); !ok {
		t.Fatalf("expected new uniqueID to be indexed")
	}

	srv.deleteGatewayIndex(devbox.Namespace, devbox.Name, devbox.Status.Network.UniqueID)
	if _, ok := srv.getGatewayIndex("new-unique-id"); ok {
		t.Fatalf("expected gateway index entry to be deleted")
	}
}
