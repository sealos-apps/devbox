package api

import "testing"

func TestBuildConfiguredSSHInfo(t *testing.T) {
	cfg := SSHConnectionConfig{
		User:                "devbox",
		Host:                "staging-usw-1.sealos.io",
		Port:                2233,
		PrivateKeySecretKey: "SEALOS_DEVBOX_PRIVATE_KEY",
	}

	info := buildConfiguredSSHInfo(cfg, "ZmFrZS1rZXk=")
	if info["target"] != "devbox@staging-usw-1.sealos.io -p 2233" {
		t.Fatalf("unexpected target: %v", info["target"])
	}
	if info["privateKeyBase64"] != "ZmFrZS1rZXk=" {
		t.Fatalf("unexpected privateKeyBase64: %v", info["privateKeyBase64"])
	}
	if info["command"] != "ssh -i <private-key-file> devbox@staging-usw-1.sealos.io -p 2233" {
		t.Fatalf("unexpected command: %v", info["command"])
	}
}
