package objectstorage

import (
	"testing"
	"time"
)

func lookupFrom(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"ORG_OBJECT_STORAGE_OCI_ENABLED":          "true",
		"ORG_OBJECT_STORAGE_OCI_TENANCY_OCID":     "ocid1.tenancy.oc1..aaaaaaaa4fugjamty3gtjrjmdmmfomlivxabqpqsgprlxkhz6lebjgutumzq",
		"ORG_OBJECT_STORAGE_OCI_USER_OCID":        "ocid1.user.oc1..aaaaaaaagdrjnpcecaqx7dzybkdvn5fvdelcm7a7fhrskywitssx5q74ezta",
		"ORG_OBJECT_STORAGE_OCI_FINGERPRINT":      "0b:a6:4b:35:a0:b2:19:73:ae:04:75:ca:17:dc:6c:ad",
		"ORG_OBJECT_STORAGE_OCI_REGION":           "sa-santiago-1",
		"ORG_OBJECT_STORAGE_OCI_NAMESPACE":        "axkhdnwe6r1c",
		"ORG_OBJECT_STORAGE_OCI_BUCKET":           "explorarte-org-knowledge-source",
		"ORG_OBJECT_STORAGE_OCI_PRIVATE_KEY_FILE": "/run/secrets/oci-object-storage-api-key.pem",
	}
}

func TestLoadConfigValid(t *testing.T) {
	cfg, err := LoadConfig(lookupFrom(validEnv()))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("expected enabled config")
	}
	if got := cfg.Host(); got != "objectstorage.sa-santiago-1.oraclecloud.com" {
		t.Fatalf("Host() = %q", got)
	}
	if got := cfg.KeyID(); got != cfg.TenancyOCID+"/"+cfg.UserOCID+"/"+cfg.Fingerprint {
		t.Fatalf("KeyID() = %q", got)
	}
	if cfg.RequestTimeout != defaultRequestTimeout {
		t.Fatalf("RequestTimeout = %v, want default", cfg.RequestTimeout)
	}
}

func TestLoadConfigDisabledSkipsValidation(t *testing.T) {
	values := map[string]string{"ORG_OBJECT_STORAGE_OCI_ENABLED": "false"}
	cfg, err := LoadConfig(lookupFrom(values))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("expected disabled config")
	}
}

func TestLoadConfigCustomTimeout(t *testing.T) {
	env := validEnv()
	env["ORG_OBJECT_STORAGE_OCI_REQUEST_TIMEOUT"] = "45s"
	cfg, err := LoadConfig(lookupFrom(env))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.RequestTimeout != 45*time.Second {
		t.Fatalf("RequestTimeout = %v, want 45s", cfg.RequestTimeout)
	}
}

func TestValidateRejectsBadTenancyOCID(t *testing.T) {
	env := validEnv()
	env["ORG_OBJECT_STORAGE_OCI_TENANCY_OCID"] = "not-an-ocid"
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for invalid tenancy OCID")
	}
}

func TestValidateRejectsBadUserOCID(t *testing.T) {
	env := validEnv()
	env["ORG_OBJECT_STORAGE_OCI_USER_OCID"] = "ocid1.tenancy.oc1..wrong"
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for user OCID using tenancy prefix")
	}
}

func TestValidateRejectsBadFingerprint(t *testing.T) {
	env := validEnv()
	env["ORG_OBJECT_STORAGE_OCI_FINGERPRINT"] = "not-a-fingerprint"
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for invalid fingerprint")
	}
}

func TestValidateRejectsMissingRegion(t *testing.T) {
	env := validEnv()
	delete(env, "ORG_OBJECT_STORAGE_OCI_REGION")
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for missing region")
	}
}

func TestValidateRejectsMissingNamespace(t *testing.T) {
	env := validEnv()
	delete(env, "ORG_OBJECT_STORAGE_OCI_NAMESPACE")
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for missing namespace")
	}
}

func TestValidateRejectsMissingBucket(t *testing.T) {
	env := validEnv()
	delete(env, "ORG_OBJECT_STORAGE_OCI_BUCKET")
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for missing bucket")
	}
}

func TestValidateRejectsRelativePrivateKeyPath(t *testing.T) {
	env := validEnv()
	env["ORG_OBJECT_STORAGE_OCI_PRIVATE_KEY_FILE"] = "relative/key.pem"
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for relative private key path")
	}
}

func TestValidateRejectsTimeoutOutOfRange(t *testing.T) {
	env := validEnv()
	env["ORG_OBJECT_STORAGE_OCI_REQUEST_TIMEOUT"] = "1h"
	if _, err := LoadConfig(lookupFrom(env)); err == nil {
		t.Fatalf("expected error for timeout above allowed range")
	}
}
