package authorization

import (
	"testing"
	"time"
)

func TestRequestHashDeterministicAndScoped(t *testing.T) {
	a := RequestHash("explorarte", 7, DigestAction([]byte("matrix")), "empresa/human", "deployment.request", "owner_or_cell_policy", "deployment", "42", DigestAction([]byte("action")), 30*time.Minute, "deploy once")
	b := RequestHash("explorarte", 7, DigestAction([]byte("matrix")), "empresa/human", "deployment.request", "owner_or_cell_policy", "deployment", "42", DigestAction([]byte("action")), 30*time.Minute, "deploy once")
	c := RequestHash("explorarte", 7, DigestAction([]byte("matrix")), "empresa/human", "deployment.request", "owner_or_cell_policy", "deployment", "43", DigestAction([]byte("action")), 30*time.Minute, "deploy once")
	if a != b || a == c {
		t.Fatalf("hashes a=%s b=%s c=%s", a, b, c)
	}
}

func TestDigestActionUsesCanonicalBytes(t *testing.T) {
	if DigestAction([]byte("a")) == DigestAction([]byte("b")) {
		t.Fatal("digest collision in fixture")
	}
	if err := ValidateDigest(DigestAction([]byte("a"))); err != nil {
		t.Fatal(err)
	}
}
