package server

import "testing"

func TestCloudOSSRequiresHTTPSAndCallbackKey(t *testing.T) {
	base := Config{MaxUploadBytes: 1 << 20, CredentialTTL: "5m", OSS: OSSConfig{
		Mode: "cloud-oss", Endpoint: "https://bucket.example", AccessKeyID: "id", AccessKeySecretEnv: "SECRET", CallbackURL: "https://server.example/callback",
	}}
	if err := base.validate(); err == nil {
		t.Fatal("missing callback public key was accepted")
	}
	base.OSS.CallbackPublicKeyFile = "callback.pem"
	base.OSS.Endpoint = "http://bucket.example"
	if err := base.validate(); err == nil {
		t.Fatal("plain HTTP OSS endpoint was accepted")
	}
	base.OSS.Endpoint = "https://bucket.example"
	if err := base.validate(); err != nil {
		t.Fatalf("valid Cloud OSS config rejected: %v", err)
	}
}
